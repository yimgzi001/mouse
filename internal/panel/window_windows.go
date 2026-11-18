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
	"gesturectl/internal/winui"
)

var (
	user     = syscall.NewLazyDLL("user32.dll")
	gdi      = syscall.NewLazyDLL("gdi32.dll")
	kernel   = syscall.NewLazyDLL("kernel32.dll")
	sendProc = user.NewProc("SendMessageW")
	post     = user.NewProc("PostMessageW")
	serial   atomic.Uint64
)

const (
	wmClose            = 0x10
	wmNotify           = 0x4e
	wmCommand          = 0x111
	wmDispatch         = 0x8001
	wmDestroy          = 2
	bmSetCheck         = 0xf1
	bmGetCheck         = 0xf0
	idPause            = 101
	idPreview          = 102
	idExit             = 103
	idTabs             = 110
	idSave             = 120
	idReload           = 121
	idNotice           = 122
	idStatus           = 123
	idHome             = 130
	idHints            = 131
	idDetails          = 132
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

// The pages, in the order of their tabs.
const (
	pageHome = iota
	pageGain
	pageEditor
	pageRecognition
	pageFiles
)

var pageTitles = []string{"手势", "灵敏度", "高级 · 绑定", "高级 · 识别", "配置文件"}

// Every position below is written for 96 DPI and scaled to the monitor. The
// base size is the smallest client area; pages start at pageTop.
const (
	baseWidth, baseHeight   = 1040, 650
	startWidth, startHeight = 1080, 690
	pageTop                 = 138
	windowStyle             = 0xcf0000
	windowExStyle           = 0x10000
)

// Anchors say what a control does with the room beyond the base size.
const (
	growWidth = 1 << iota
	growHeight
	moveRight
	moveDown
)

const (
	fontBody = iota
	fontTitle
	fontList
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
type tabItem struct {
	Mask, State, StateMask uint32
	Text                   *uint16
	TextMax, Image         int32
	Param                  uintptr
}

type Options struct {
	Snapshot      func() control.Snapshot
	Apply         func(uint64, control.Configuration) (control.Snapshot, error)
	Paused        func() bool
	SetPaused     func(bool)
	TogglePreview func()
	Exit          func()
	// Hidden keeps the panel out of sight until Show, for a program that
	// starts in the background.
	Hidden bool
}

type layoutItem struct {
	handle                    uintptr
	page, x, y, width, height int
	anchor, font              int
	// at and shown are what layout last gave the control; shown is zero until
	// layout has seen it.
	at    [4]int
	shown int
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
	dpi                             int
	fonts                           [3]uintptr
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

// later runs fn on the window thread once the message in hand is done: a
// control must not be rebuilt inside its own notification.
func (w *Window) later(fn func()) {
	w.mu.Lock()
	w.tasks = append(w.tasks, fn)
	w.mu.Unlock()
	post.Call(w.hwnd, wmDispatch, 0, 0)
}
func (w *Window) Show() {
	w.invoke(func() {
		// A panel that started hidden meets STARTUPINFO on its first show,
		// which may hide it again; the second show is the one that counts.
		user.NewProc("ShowWindow").Call(w.hwnd, 9)
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
	winui.EnableVisualStyles()
	move := kernel.NewProc("RtlMoveMemory")
	instance, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
	name := utf16(fmt.Sprintf("GestureMousePanel_%d", serial.Add(1)))
	cursor, _, _ := user.NewProc("LoadCursorW").Call(0, 32512)
	callback := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case 0x24:
			if w.dpi == 0 {
				break
			}
			// lParam points to memory owned by user32, not the Go heap. Copy
			// through the Win32 API rather than manufacturing a Go pointer.
			width, height := w.frame(baseWidth, baseHeight)
			minimum := point{int32(width), int32(height)}
			move.Call(lp+unsafe.Offsetof(minMaxInfo{}.MinTrackSize), uintptr(unsafe.Pointer(&minimum)), unsafe.Sizeof(minimum))
			return 0
		case 5:
			if w.controls[idSave] != 0 {
				w.layout()
			}
			return 0
		case 0x2e0:
			// The window moved to a monitor with another scaling; lParam is
			// the rectangle Windows suggests for it there.
			var suggested rect
			move.Call(uintptr(unsafe.Pointer(&suggested)), lp, unsafe.Sizeof(suggested))
			w.dpi = int(uint16(wp))
			user.NewProc("SetWindowPos").Call(hwnd, 0, uintptr(suggested.Left), uintptr(suggested.Top), uintptr(suggested.Right-suggested.Left), uintptr(suggested.Bottom-suggested.Top), 0x14)
			w.rescale()
			return 0
		case wmCommand:
			w.command(int(uint16(wp)), int(uint16(wp>>16)))
			return 0
		case wmNotify:
			var n listNotify
			move.Call(uintptr(unsafe.Pointer(&n.Header)), lp, unsafe.Sizeof(n.Header))
			if n.Header.From != 0 && n.Header.From == w.controls[idHome] && (n.Header.Code == lvnItemChanged || n.Header.Code == nmDoubleClick) {
				move.Call(uintptr(unsafe.Pointer(&n)), lp, unsafe.Sizeof(n))
			}
			return w.notify(&n)
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
	hwnd, _, err := user.NewProc("CreateWindowExW").Call(windowExStyle, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(utf16("手势鼠标 · 配置"))), windowStyle, 0x80000000, 0x80000000, startWidth, startHeight, 0, 0, instance, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("创建原生配置窗口：%w", err)
		return
	}
	w.mu.Lock()
	w.hwnd = hwnd
	w.mu.Unlock()
	w.dpi = winui.DPI(hwnd)
	w.makeFonts()
	defer func() {
		for _, font := range w.fonts {
			gdi.NewProc("DeleteObject").Call(font)
		}
	}()
	if err := w.createControls(); err != nil {
		user.NewProc("DestroyWindow").Call(hwnd)
		ready <- err
		return
	}
	w.place()
	w.renderAll()
	user.NewProc("SetTimer").Call(hwnd, 1, 500, 0)
	if !w.options.Hidden {
		user.NewProc("ShowWindow").Call(hwnd, 5)
		// STARTUPINFO may hide the first ShowWindow when only the console was
		// launched hidden. The second explicit show reveals the native panel.
		user.NewProc("ShowWindow").Call(hwnd, 5)
	}
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
func (w *Window) scale(v int) int { return winui.Scale(v, w.dpi) }

// frame returns the size of the window whose client area measures width by
// height at 96 DPI.
func (w *Window) frame(width, height int) (int, int) {
	r := rect{Right: int32(w.scale(width)), Bottom: int32(w.scale(height))}
	if adjust := user.NewProc("AdjustWindowRectExForDpi"); adjust.Find() == nil {
		adjust.Call(uintptr(unsafe.Pointer(&r)), windowStyle, 0, windowExStyle, uintptr(w.dpi))
	} else {
		user.NewProc("AdjustWindowRectEx").Call(uintptr(unsafe.Pointer(&r)), windowStyle, 0, windowExStyle)
	}
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

// place gives the window its starting size at the scaling of its monitor, no
// larger than the work area, and centres it there.
func (w *Window) place() {
	width, height := w.frame(startWidth, startHeight)
	monitor, _, _ := user.NewProc("MonitorFromWindow").Call(w.hwnd, 2)
	info := struct {
		Size          uint32
		Monitor, Work rect
		Flags         uint32
	}{Size: 40}
	if ok, _, _ := user.NewProc("GetMonitorInfoW").Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		user.NewProc("SetWindowPos").Call(w.hwnd, 0, 0, 0, arg(width), arg(height), 0x16)
		return
	}
	roomWidth, roomHeight := int(info.Work.Right-info.Work.Left), int(info.Work.Bottom-info.Work.Top)
	width, height = min(width, roomWidth), min(height, roomHeight)
	user.NewProc("SetWindowPos").Call(w.hwnd, 0, arg(int(info.Work.Left)+(roomWidth-width)/2), arg(int(info.Work.Top)+(roomHeight-height)/2), arg(width), arg(height), 0x14)
}

// makeFonts creates the fonts for the current scaling and hands them to the
// controls.
func (w *Window) makeFonts() {
	old := w.fonts
	const face = "Microsoft YaHei UI"
	w.fonts = [3]uintptr{
		fontBody:  winui.Font(9, w.dpi, 400, winui.ClearType, face),
		fontTitle: winui.Font(15, w.dpi, 600, winui.ClearType, face),
		fontList:  winui.Font(10, w.dpi, 400, winui.ClearType, face),
	}
	for _, item := range w.layouts {
		send(item.handle, 0x30, w.fonts[item.font], 1)
	}
	for _, font := range old {
		if font != 0 {
			gdi.NewProc("DeleteObject").Call(font)
		}
	}
}

// rescale redoes what depends on the scaling after w.dpi changed.
func (w *Window) rescale() {
	w.makeFonts()
	w.renderHome()
	w.layout()
}
func (w *Window) add(class, text string, id, page, x, y, width, height int, style uintptr) uintptr {
	if id == 0 {
		w.nextID++
		id = w.nextID
	}
	ex := uintptr(0)
	if class == "EDIT" || class == "LISTBOX" || class == "SysListView32" {
		ex = 0x200
	}
	hwnd, _, _ := user.NewProc("CreateWindowExW").Call(ex, uintptr(unsafe.Pointer(utf16(class))), uintptr(unsafe.Pointer(utf16(text))), 0x50000000|style, arg(w.scale(x)), arg(w.scale(y)), arg(w.scale(width)), arg(w.scale(height)), w.hwnd, uintptr(id), 0, 0)
	w.controls[id] = hwnd
	send(hwnd, 0x30, w.fonts[fontBody], 1)
	w.layouts = append(w.layouts, layoutItem{handle: hwnd, page: page, x: x, y: y, width: width, height: height})
	return hwnd
}

// pin sets how the control added last follows the window size.
func (w *Window) pin(anchor int) { w.layouts[len(w.layouts)-1].anchor = anchor }

// useFont gives the control added last another of the window's fonts.
func (w *Window) useFont(font int) {
	item := &w.layouts[len(w.layouts)-1]
	item.font = font
	send(item.handle, 0x30, w.fonts[font], 1)
}
func (w *Window) label(text string, page, x, y, width, height int) uintptr {
	return w.add("STATIC", text, 0, page, x, y, width, height, 0)
}
func (w *Window) button(text string, id, page, x, y, width int) uintptr {
	return w.add("BUTTON", text, id, page, x, y, width, 28, 0x10000)
}
func (w *Window) edit(id, page, x, y, width int) uintptr {
	h := w.add("EDIT", "", id, page, x, y, width, 24, 0x10080)
	send(h, 0xc5, 1000, 0)
	return h
}
func (w *Window) combo(id, page, x, y, width int) uintptr {
	return w.add("COMBOBOX", "", id, page, x, y, width, 350, 0x210403)
}
func (w *Window) createControls() error {
	w.label("手势鼠标", -1, 24, 8, 200, 34)
	w.useFont(fontTitle)
	w.label("F7 打开面板 · F8 暂停/恢复 · F9 退出", -1, 232, 20, 400, 20)
	w.button("暂停手势控制", idPause, -1, 650, 12, 140)
	w.pin(moveRight)
	w.button("实时预览", idPreview, -1, 800, 12, 116)
	w.pin(moveRight)
	w.button("退出", idExit, -1, 926, 12, 90)
	w.pin(moveRight)
	w.add("STATIC", "等待摄像头状态…", idStatus, -1, 24, 50, 992, 38, 0)
	w.pin(growWidth)
	tabs := w.add("SysTabControl32", "", idTabs, -1, 24, 96, 992, 30, 0x10000)
	w.pin(growWidth)
	send(tabs, 0x132b, 0, uintptr(w.scale(14))|uintptr(w.scale(5))<<16)
	for i, title := range pageTitles {
		item := tabItem{Mask: 1, Text: utf16(title)}
		send(tabs, 0x133e, uintptr(i), uintptr(unsafe.Pointer(&item)))
	}
	// Only the row of tabs shows; the pages are laid out on the window itself.
	var row rect
	if send(tabs, 0x130a, 0, uintptr(unsafe.Pointer(&row))) != 0 {
		w.layouts[len(w.layouts)-1].height = (int(row.Bottom)*96+w.dpi-1)/w.dpi + 2
	}
	w.add("STATIC", "首页的勾选马上生效；其他修改先进入草稿，保存后生效。关闭面板继续在后台运行，F7 可重开。", idNotice, -1, 24, 600, 590, 38, 0)
	w.pin(growWidth | moveDown)
	w.button("撤销并重载运行配置", idReload, -1, 626, 606, 200)
	w.pin(moveRight | moveDown)
	w.add("BUTTON", "保存并应用", idSave, -1, 836, 606, 180, 28, 0x10001)
	w.pin(moveRight | moveDown)
	w.createHome()
	w.createSettings()
	w.createEditor()
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
	extraWidth, extraHeight := max(0, int(r.Right)-w.scale(baseWidth)), max(0, int(r.Bottom)-w.scale(baseHeight))
	for i := range w.layouts {
		item := &w.layouts[i]
		x, y, width, height := w.scale(item.x), w.scale(item.y), w.scale(item.width), w.scale(item.height)
		if item.page >= 0 {
			y += w.scale(pageTop)
		}
		if item.anchor&moveRight != 0 {
			x += extraWidth
		}
		if item.anchor&growWidth != 0 {
			width += extraWidth
		}
		if item.anchor&moveDown != 0 {
			y += extraHeight
		}
		if item.anchor&growHeight != 0 {
			height += extraHeight
		}
		// Controls that stay where they are are left alone, so that a refresh
		// does not make the whole window flicker.
		if at := [4]int{x, y, width, height}; at != item.at {
			user.NewProc("MoveWindow").Call(item.handle, arg(x), arg(y), arg(width), arg(height), 1)
			item.at = at
		}
		shown := 1
		if item.page < 0 || item.page == w.page {
			shown = 2
		}
		if shown != item.shown {
			user.NewProc("ShowWindow").Call(item.handle, uintptr(5*(shown-1)))
			item.shown = shown
		}
	}
	w.sizeColumns()
}

// showPage switches to a page as its tab would.
func (w *Window) showPage(page int) {
	w.page = page
	send(w.controls[idTabs], 0x130c, uintptr(page), 0)
	w.layout()
}

// notify handles WM_NOTIFY from the tabs and from the home list.
func (w *Window) notify(n *listNotify) uintptr {
	switch {
	case n.Header.From == 0:
	case n.Header.From == w.controls[idTabs] && n.Header.Code == tcnSelChanging:
		if err := w.commitAll(); err != nil {
			w.notice("请先修正当前表单：" + err.Error())
			return 1
		}
	case n.Header.From == w.controls[idTabs] && n.Header.Code == tcnSelChange:
		w.page = int(int32(send(w.controls[idTabs], 0x130b, 0, 0)))
		w.layout()
	case n.Header.From == w.controls[idHome] && !w.loading:
		w.homeNotify(n)
	}
	return 0
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
		if textOf(w.controls[idPause]) != title {
			setText(w.controls[idPause], title)
		}
	}
	w.mu.Lock()
	status := w.status
	w.mu.Unlock()
	if status != "" && textOf(w.controls[idStatus]) != status {
		setText(w.controls[idStatus], status)
	}
}
func (w *Window) renderAll() {
	w.loading = true
	defer func() { w.loading = false }()
	w.renderBindings()
	w.renderSettings()
	w.renderHome()
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
	case idHints:
		hide := send(w.controls[idHints], bmGetCheck, 0, 0) == 0
		done := "屏幕提示已打开。"
		if hide {
			done = "屏幕提示已关闭。"
		}
		w.quick(func(c *control.Configuration) { c.Preferences.HideHints = hide }, done)
	case idDetails:
		w.openEditor(w.selected)
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
		// Only the controls of the forms change the draft. The dialog keys
		// Enter and Esc arrive here too, as commands 1 and 2.
		if id >= idList && (notification == 0 || notification == 0x300 || notification == 1) {
			w.formDirty = true
			w.notice("有未保存修改；保存并应用后生效。")
			w.updateGainHelp()
		}
	}
}
