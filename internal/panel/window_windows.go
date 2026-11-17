//go:build windows && amd64

// Package panel provides the native Windows configuration editor. It has no
// HTTP listener, WebView, camera access, or input injection capability.
package panel

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gesturectl/internal/control"
	"gesturectl/internal/engine"
)

var (
	user     = syscall.NewLazyDLL("user32.dll")
	gdi      = syscall.NewLazyDLL("gdi32.dll")
	sendProc = user.NewProc("SendMessageW")
	post     = user.NewProc("PostMessageW")
	serial   atomic.Uint64
)

const (
	wmClose            = 0x10
	wmCommand          = 0x111
	wmDispatch         = 0x8001
	wmDestroy          = 2
	bmSetCheck         = 0xf1
	bmGetCheck         = 0xf0
	idPause            = 101
	idPreview          = 102
	idExit             = 103
	idBindings         = 110
	idSettings         = 111
	idFiles            = 112
	idSave             = 120
	idReload           = 121
	idNotice           = 122
	idStatus           = 123
	idList             = 200
	idNew              = 201
	idDelete           = 202
	idEnabled          = 203
	idName             = 204
	idHand             = 205
	idPose             = 206
	idTrigger          = 207
	idHold             = 208
	idInterval         = 209
	idAction           = 210
	idPoseHelp         = 211
	idActionHelp       = 212
	idRelease          = 213
	idGainHelp         = 214
	idImport           = 300
	idExport           = 301
	idResetBindings    = 302
	idResetPreferences = 303
	idResetRecognition = 304
)

type rect struct{ Left, Top, Right, Bottom int32 }
type point struct{ X, Y int32 }
type message struct {
	Window         uintptr
	ID             uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}
type windowClass struct {
	Style                              uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
}
type minMaxInfo struct{ Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize point }

type Options struct {
	Snapshot      func() control.Snapshot
	Apply         func(uint64, control.Configuration) (control.Snapshot, error)
	Paused        func() bool
	SetPaused     func(bool)
	TogglePreview func()
	Exit          func()
}

type layoutItem struct {
	handle                    uintptr
	page, x, y, width, height int
	right                     bool
}
type paramControl struct {
	spec   engine.ParamSpec
	handle uintptr
	values []string
}
type Window struct {
	mu                              sync.Mutex
	hwnd                            uintptr
	done                            chan struct{}
	tasks                           []func()
	status                          string
	options                         Options
	session                         *draft
	controls                        map[int]uintptr
	layouts                         []layoutItem
	page, selected                  int
	loading, formDirty              bool
	font                            uintptr
	nextID                          int
	hands, poses, actions, triggers []string
	releasePoses                    []string
	params                          []paramControl
	paramLabels                     []uintptr
	settingEdits                    []uintptr
}

func New(options Options) (*Window, error) {
	if options.Snapshot == nil || options.Apply == nil {
		return nil, fmt.Errorf("配置面板需要读取和保存回调")
	}
	snapshot := options.Snapshot()
	cfg := snapshot.Config.Clone()
	if err := cfg.Normalize(); err != nil {
		return nil, fmt.Errorf("初始化配置面板：%w", err)
	}
	snapshot.Config = cfg
	w := &Window{done: make(chan struct{}), options: options, session: newDraft(snapshot), controls: map[int]uintptr{}, selected: -1, nextID: 1000}
	ready := make(chan error, 1)
	go w.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return w, nil
}

// invoke marshals work to the window's owning thread without holding a Go lock
// across a Win32 call or a caller callback.
func (w *Window) invoke(fn func()) {
	finished := make(chan struct{})
	w.mu.Lock()
	hwnd := w.hwnd
	if hwnd != 0 {
		w.tasks = append(w.tasks, func() { defer close(finished); fn() })
	}
	w.mu.Unlock()
	if hwnd == 0 {
		return
	}
	post.Call(hwnd, wmDispatch, 0, 0)
	select {
	case <-finished:
	case <-w.done:
	}
}
func (w *Window) Show() {
	w.invoke(func() {
		user.NewProc("ShowWindow").Call(w.hwnd, 9)
		user.NewProc("SetForegroundWindow").Call(w.hwnd)
		w.refresh()
	})
}
func (w *Window) PublishStatus(status string) { w.mu.Lock(); w.status = status; w.mu.Unlock() }

// Close is the forced program shutdown path; WM_CLOSE from the title bar hides.
func (w *Window) Close() {
	w.mu.Lock()
	hwnd := w.hwnd
	w.mu.Unlock()
	if hwnd != 0 {
		post.Call(hwnd, 0x8002, 0, 0)
	}
	<-w.done
}
func (w *Window) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(w.done)
	instance, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	name := utf16(fmt.Sprintf("GestureMousePanel_%d", serial.Add(1)))
	cursor, _, _ := user.NewProc("LoadCursorW").Call(0, 32512)
	callback := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case 0x24:
			// lParam points to memory owned by user32, not the Go heap. Copy
			// through the Win32 API rather than manufacturing a Go pointer.
			minimum := point{1120, 850}
			syscall.NewLazyDLL("kernel32.dll").NewProc("RtlMoveMemory").Call(lp+unsafe.Offsetof(minMaxInfo{}.MinTrackSize), uintptr(unsafe.Pointer(&minimum)), unsafe.Sizeof(minimum))
			return 0
		case 5:
			if w.controls[idSave] != 0 {
				w.layout()
			}
			return 0
		case wmCommand:
			w.command(int(uint16(wp)), int(uint16(wp>>16)))
			return 0
		case 0x113:
			visible, _, _ := user.NewProc("IsWindowVisible").Call(hwnd)
			if visible != 0 {
				w.refresh()
			}
			return 0
		case wmClose:
			user.NewProc("ShowWindow").Call(hwnd, 0)
			return 0
		case 0x8002:
			user.NewProc("DestroyWindow").Call(hwnd)
			return 0
		case wmDestroy:
			w.mu.Lock()
			w.hwnd = 0
			w.mu.Unlock()
			user.NewProc("PostQuitMessage").Call(0)
			return 0
		case wmDispatch:
			w.mu.Lock()
			tasks := w.tasks
			w.tasks = nil
			w.mu.Unlock()
			for _, fn := range tasks {
				fn()
			}
			return 0
		}
		result, _, _ := user.NewProc("DefWindowProcW").Call(hwnd, uintptr(msg), wp, lp)
		return result
	})
	wc := windowClass{Proc: callback, Instance: instance, Cursor: cursor, Background: 16, Name: name}
	atom, _, err := user.NewProc("RegisterClassW").Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		ready <- fmt.Errorf("注册原生配置窗口：%w", err)
		return
	}
	defer user.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	hwnd, _, err := user.NewProc("CreateWindowExW").Call(0x10000, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(utf16("手势鼠标 · 配置"))), 0xcf0000, 0x80000000, 0x80000000, 1160, 860, 0, 0, instance, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("创建原生配置窗口：%w", err)
		return
	}
	w.mu.Lock()
	w.hwnd = hwnd
	w.mu.Unlock()
	w.font, _, _ = gdi.NewProc("CreateFontW").Call(arg(-16), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16("Segoe UI"))))
	defer gdi.NewProc("DeleteObject").Call(w.font)
	if err := w.createControls(); err != nil {
		user.NewProc("DestroyWindow").Call(hwnd)
		ready <- err
		return
	}
	w.renderAll()
	w.layout()
	user.NewProc("SetTimer").Call(hwnd, 1, 500, 0)
	user.NewProc("ShowWindow").Call(hwnd, 5)
	// STARTUPINFO may hide the first ShowWindow when only the console was
	// launched hidden. The second explicit show reveals the native panel.
	user.NewProc("ShowWindow").Call(hwnd, 5)
	ready <- nil
	var msg message
	for {
		r, _, _ := user.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		handled, _, _ := user.NewProc("IsDialogMessageW").Call(hwnd, uintptr(unsafe.Pointer(&msg)))
		if handled == 0 {
			user.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
			user.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
		}
	}
}
func utf16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func arg(v int) uintptr      { return uintptr(v) }
func send(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	r, _, _ := sendProc.Call(hwnd, uintptr(msg), wp, lp)
	return r
}
func setText(hwnd uintptr, s string) {
	user.NewProc("SetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(utf16(s))))
}
func textOf(hwnd uintptr) string {
	n, _, _ := user.NewProc("GetWindowTextLengthW").Call(hwnd)
	b := make([]uint16, n+1)
	user.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	return syscall.UTF16ToString(b)
}
func (w *Window) add(class, text string, id, page, x, y, width, height int, style uintptr) uintptr {
	if id == 0 {
		w.nextID++
		id = w.nextID
	}
	ex := uintptr(0)
	if class == "EDIT" || class == "LISTBOX" {
		ex = 0x200
	}
	hwnd, _, _ := user.NewProc("CreateWindowExW").Call(ex, uintptr(unsafe.Pointer(utf16(class))), uintptr(unsafe.Pointer(utf16(text))), 0x50000000|style, arg(x), arg(y), arg(width), arg(height), w.hwnd, uintptr(id), 0, 0)
	w.controls[id] = hwnd
	send(hwnd, 0x30, w.font, 1)
	w.layouts = append(w.layouts, layoutItem{handle: hwnd, page: page, x: x, y: y, width: width, height: height})
	return hwnd
}
func (w *Window) label(text string, page, x, y, width, height int) uintptr {
	return w.add("STATIC", text, 0, page, x, y, width, height, 0)
}
func (w *Window) button(text string, id, page, x, y, width int) uintptr {
	return w.add("BUTTON", text, id, page, x, y, width, 30, 0x10000)
}
func (w *Window) edit(id, page, x, y, width int) uintptr {
	h := w.add("EDIT", "", id, page, x, y, width, 27, 0x10080)
	send(h, 0xc5, 1000, 0)
	return h
}
func (w *Window) combo(id, page, x, y, width int) uintptr {
	return w.add("COMBOBOX", "", id, page, x, y, width, 350, 0x210103)
}
func (w *Window) createControls() error {
	w.label("手势鼠标", -1, 20, 15, 260, 30)
	w.label("F7 打开面板 · F8 暂停/恢复 · F9 退出", -1, 290, 18, 400, 24)
	w.button("暂停手势控制", idPause, -1, 704, 12, 140)
	w.button("实时预览", idPreview, -1, 854, 12, 116)
	w.button("退出", idExit, -1, 980, 12, 90)
	w.add("STATIC", "等待摄像头状态…", idStatus, -1, 20, 52, 1080, 40, 0)
	for i, title := range []string{"手势绑定", "灵敏度与识别", "配置管理"} {
		w.button(title, idBindings+i, -1, 20+i*190, 106, 180)
	}
	w.add("STATIC", "修改先进入草稿；保存后生效。关闭面板继续在后台运行，F7 可重开。", idNotice, -1, 20, 700, 800, 40, 0)
	w.button("撤销并重载运行配置", idReload, -1, 690, 750, 210)
	w.add("BUTTON", "保存并应用", idSave, -1, 912, 750, 180, 32, 0x10001)
	w.createEditor()
	w.createSettings()
	w.createFiles()
	for id, h := range w.controls {
		if h == 0 {
			return fmt.Errorf("创建配置控件 %d 失败", id)
		}
	}
	return nil
}
func (w *Window) layout() {
	var r rect
	user.NewProc("GetClientRect").Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	width, height := int(r.Right), int(r.Bottom)
	extra := max(0, width-1120)
	for _, item := range w.layouts {
		x, y, ww, hh := item.x, item.y, item.width, item.height
		if item.page < 0 {
			switch item.handle {
			case w.controls[idPause], w.controls[idPreview], w.controls[idExit]:
				x += extra
			case w.controls[idStatus]:
				ww += extra
			case w.controls[idSave], w.controls[idReload]:
				x += extra
				y = height - 54
			case w.controls[idNotice]:
				y = height - 102
				ww = width - 40
			}
		} else {
			y += 152
			if x >= 384 && ww >= 300 {
				ww += extra
			}
		}
		user.NewProc("MoveWindow").Call(item.handle, arg(x), arg(y), arg(ww), arg(hh), 1)
		show := uintptr(0)
		if item.page < 0 || item.page == w.page {
			show = 5
		}
		user.NewProc("ShowWindow").Call(item.handle, show)
	}
}
func (w *Window) notice(s string) { setText(w.controls[idNotice], s) }
func (w *Window) refresh() {
	s := w.options.Snapshot()
	if s.Revision != w.session.revision {
		if w.formDirty || w.session.dirty() {
			w.session.stale = true
			w.notice("运行配置已在其他入口更新。当前草稿保留；保存将进行版本检查，请先导出草稿或撤销并重载。")
		} else if w.session.observe(s) {
			w.renderAll()
		}
	}
	if w.options.Paused != nil {
		title := "暂停手势控制"
		if w.options.Paused() {
			title = "恢复手势控制"
		}
		setText(w.controls[idPause], title)
	}
	w.mu.Lock()
	status := w.status
	w.mu.Unlock()
	if status != "" {
		setText(w.controls[idStatus], status)
	}
}
func (w *Window) renderAll() {
	w.loading = true
	defer func() { w.loading = false }()
	w.renderBindings()
	w.renderSettings()
	w.layout()
	w.formDirty = false
}
func (w *Window) command(id, notification int) {
	if w.loading {
		return
	}
	switch id {
	case idPause:
		if w.options.SetPaused != nil && w.options.Paused != nil {
			w.options.SetPaused(!w.options.Paused())
			w.refresh()
		}
	case idPreview:
		if w.options.TogglePreview != nil {
			w.options.TogglePreview()
		}
	case idExit:
		if w.options.Exit != nil {
			go w.options.Exit()
		}
	case idBindings, idSettings, idFiles:
		if err := w.commitAll(); err != nil {
			w.notice("请先修正当前表单：" + err.Error())
			return
		}
		w.page = id - idBindings
		w.layout()
	case idSave:
		if err := w.commitAll(); err != nil {
			w.notice("未保存：" + err.Error())
			return
		}
		if err := w.session.apply(w.options.Apply); err != nil {
			w.notice("保存失败，草稿已保留：" + err.Error())
			return
		}
		w.renderAll()
		w.notice("已保存并应用；暂停状态保持不变。")
	case idReload:
		w.session.replace(w.options.Snapshot())
		w.renderAll()
		w.notice("已重载当前运行配置。")
	case idList:
		if notification == 1 {
			index := int(int32(send(w.controls[idList], 0x188, 0, 0)))
			if err := w.commitBinding(); err != nil {
				send(w.controls[idList], 0x186, arg(w.selected), 0)
				w.notice("切换前请修正：" + err.Error())
				return
			}
			w.selectBinding(index)
		}
	case idNew:
		if err := w.commitAll(); err != nil {
			w.notice(err.Error())
			return
		}
		w.newBinding()
	case idDelete:
		if w.selected >= 0 && w.selected < len(w.session.config.Bindings) {
			i := w.selected
			w.session.config.Bindings = append(w.session.config.Bindings[:i], w.session.config.Bindings[i+1:]...)
			w.selected = min(i, len(w.session.config.Bindings)-1)
			w.renderAll()
			w.notice("绑定已删除，保存并应用后生效。")
		}
	case idAction:
		if notification == 1 {
			w.changeAction()
		}
	case idHand, idPose:
		if notification == 1 {
			w.changeInput(id)
		}
	case idTrigger:
		if notification == 1 {
			w.formDirty = true
			w.updateTriggerHelp()
		}
	case idImport, idExport, idResetBindings, idResetPreferences, idResetRecognition:
		w.fileCommand(id)
	default:
		if notification == 0 || notification == 0x300 || notification == 1 {
			w.formDirty = true
			w.notice("有未保存修改；保存并应用后生效。")
			w.updateGainHelp()
		}
	}
}
