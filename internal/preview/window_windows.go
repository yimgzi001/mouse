//go:build windows && amd64

// Package preview displays live camera frames and hand states in a native
// Windows window. Only the latest frame is retained; no image is saved.
package preview

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"gesturectl/internal/control"
	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
	"gesturectl/internal/vision"
)

var (
	user = syscall.NewLazyDLL("user32.dll")
	gdi  = syscall.NewLazyDLL("gdi32.dll")
	post = user.NewProc("PostMessageW")
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
type paintStruct struct {
	DC                 uintptr
	Erase              int32
	Rect               rect
	Restore, Increment int32
	Reserved           [32]byte
}
type bitmapInfo struct {
	Size                   uint32
	Width, Height          int32
	Planes, Bits           uint16
	Compression, ImageSize uint32
	XPixels, YPixels       int32
	Colors, Important      uint32
}

type Window struct {
	mu                               sync.Mutex
	hwnd                             uintptr
	pixels                           []byte
	width, height                    int
	states                           []gesture.HandState
	paused, swapped                  bool
	dryRun                           bool
	sensitivity                      float64
	done                             chan struct{}
	onClose                          func()
	running                          map[int]string
	lastAction                       string
	preferences                      control.Preferences
	onPreferences                    func(control.Preferences) error
	onPreferencePatch                func(control.Preferences, control.Preferences) (control.Preferences, error)
	pendingPreferences               control.Preferences
	sliders                          []sensitivitySlider
	settingsTitle                    uintptr
	visible, hideOnClose, destroying bool
}

type Options struct {
	Preferences       control.Preferences
	OnPreferences     func(control.Preferences) error
	OnPreferencePatch func(control.Preferences, control.Preferences) (control.Preferences, error)
	OnClose           func()
	Visible           bool
	HideOnClose       bool
}

// SetActions copies the current actions; discrete events remain visible after
// their gesture is released, so users can distinguish recognition from firing.
func (p *Window) SetActions(running map[int]string, events []engine.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = make(map[int]string, len(running))
	for id, name := range running {
		p.running[id] = name
	}
	if len(events) > 0 {
		ev := events[len(events)-1]
		verb := map[string]string{"start": "开始", "fire": "触发", "end": "结束", "cancel": "取消", "confirm": "确认"}[ev.What]
		p.lastAction = fmt.Sprintf("最近动作：%s %s", ev.Binding, verb)
	}
}

func New(onClose func()) (*Window, error) {
	return NewWithPreferences(onClose, control.DefaultPreferences(), nil)
}

func NewWithPreferences(onClose func(), preferences control.Preferences, onPreferences func(control.Preferences) error) (*Window, error) {
	return NewWithOptions(Options{Preferences: preferences, OnPreferences: onPreferences, OnClose: onClose, Visible: true})
}

func NewWithOptions(opt Options) (*Window, error) {
	if err := opt.Preferences.Validate(); err != nil {
		return nil, err
	}
	p := &Window{done: make(chan struct{}), onClose: opt.OnClose, preferences: opt.Preferences, onPreferences: opt.OnPreferences, onPreferencePatch: opt.OnPreferencePatch, visible: opt.Visible, hideOnClose: opt.HideOnClose}
	ready := make(chan error, 1)
	go p.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return p, nil
}

// Publish copies the borrowed camera frame so the capture buffer can be reused.
func (p *Window) Publish(frame *vision.Frame, states []gesture.HandState, paused, swapped bool, sensitivity float64, dryRun bool) {
	if frame == nil || frame.W <= 0 || frame.H <= 0 || len(frame.Pix) < frame.W*frame.H*3 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hwnd == 0 || !p.visible {
		return
	}
	size := frame.W * frame.H * 4
	if cap(p.pixels) < size {
		p.pixels = make([]byte, size)
	} else {
		p.pixels = p.pixels[:size]
	}
	for s, d := 0, 0; s < frame.W*frame.H*3; s, d = s+3, d+4 {
		p.pixels[d], p.pixels[d+1], p.pixels[d+2], p.pixels[d+3] = frame.Pix[s+2], frame.Pix[s+1], frame.Pix[s], 0
	}
	p.width, p.height = frame.W, frame.H
	p.states = append(p.states[:0], states...)
	p.paused, p.swapped, p.sensitivity = paused, swapped, sensitivity
	p.preferences.SwapHands = swapped
	p.dryRun = dryRun
	post.Call(p.hwnd, 0x8001, 0, 0)
}

func (p *Window) Close() {
	p.mu.Lock()
	hwnd := p.hwnd
	p.destroying = true
	p.mu.Unlock()
	if hwnd != 0 {
		post.Call(hwnd, 0x0010, 0, 0)
	}
	<-p.done
}

func (p *Window) Visible() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.visible && p.hwnd != 0
}

func (p *Window) show(visible bool) {
	p.mu.Lock()
	hwnd := p.hwnd
	p.mu.Unlock()
	value := uintptr(0)
	if visible {
		value = 1
	}
	if hwnd != 0 {
		post.Call(hwnd, 0x8002, value, 0)
	}
}

func (p *Window) Show() { p.show(true) }
func (p *Window) Hide() { p.show(false) }

func (p *Window) SetPreferences(preferences control.Preferences) {
	p.mu.Lock()
	p.pendingPreferences = preferences
	hwnd := p.hwnd
	p.mu.Unlock()
	if hwnd != 0 {
		post.Call(hwnd, 0x8003, 0, 0)
	}
}

func (p *Window) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.done)
	defWindow := user.NewProc("DefWindowProcW")
	callback := syscall.NewCallback(func(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
		switch msg {
		case 0x0005:
			p.layoutSettings(hwnd)
			return 0
		case 0x0114:
			p.scrollSetting(hwnd, uint16(wparam), lparam)
			return 0
		case 0x8001:
			user.NewProc("InvalidateRect").Call(hwnd, 0, 0)
			return 0
		case 0x8002:
			p.mu.Lock()
			p.visible = wparam != 0
			p.mu.Unlock()
			command := uintptr(0)
			if wparam != 0 {
				command = 5
			}
			user.NewProc("ShowWindow").Call(hwnd, command)
			return 0
		case 0x8003:
			p.mu.Lock()
			next := p.pendingPreferences
			p.preferences = next
			p.mu.Unlock()
			p.displayPreferences(next)
			return 0
		case 0x000F:
			p.paint(hwnd)
			return 0
		case 0x0010:
			p.mu.Lock()
			hide := p.hideOnClose && !p.destroying
			p.visible = false
			p.mu.Unlock()
			if hide {
				user.NewProc("ShowWindow").Call(hwnd, 0)
				return 0
			}
			if p.onClose != nil {
				p.onClose()
			}
			user.NewProc("DestroyWindow").Call(hwnd)
			return 0
		case 0x0002:
			p.mu.Lock()
			p.hwnd = 0
			p.visible = false
			p.mu.Unlock()
			user.NewProc("PostQuitMessage").Call(0)
			return 0
		case 0x0014:
			return 1 // paint draws both image and footer
		}
		v, _, _ := defWindow.Call(hwnd, uintptr(msg), wparam, lparam)
		return v
	})
	className, _ := syscall.UTF16PtrFromString("GestureMousePreview")
	title, _ := syscall.UTF16PtrFromString("手势鼠标 · 实时识别")
	instance, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := user.NewProc("LoadCursorW").Call(0, 32512)
	wc := windowClass{Proc: callback, Instance: instance, Cursor: cursor, Background: 6, Name: className}
	atom, _, err := user.NewProc("RegisterClassW").Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		ready <- fmt.Errorf("register preview window: %w", err)
		return
	}
	defer user.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(className)), instance)
	screenWidth, _, _ := user.NewProc("GetSystemMetrics").Call(0)
	screenHeight, _, _ := user.NewProc("GetSystemMetrics").Call(1)
	hwnd, _, err := user.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), 0x02CF0000, 40, 40, uintptr(min(900, max(640, int(screenWidth)-80))), uintptr(min(900, max(480, int(screenHeight)-100))), 0, 0, instance, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("create preview window: %w", err)
		return
	}
	p.mu.Lock()
	p.hwnd = hwnd
	p.mu.Unlock()
	if err := p.createSettings(hwnd); err != nil {
		user.NewProc("DestroyWindow").Call(hwnd)
		ready <- err
		return
	}
	if p.visible {
		user.NewProc("ShowWindow").Call(hwnd, 5)
		// A hidden console's STARTUPINFO can override the first call.
		user.NewProc("ShowWindow").Call(hwnd, 5)
	}
	ready <- nil
	getMessage := user.NewProc("GetMessageW")
	translate := user.NewProc("TranslateMessage")
	dispatch := user.NewProc("DispatchMessageW")
	var msg message
	for {
		ok, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ok) <= 0 {
			return
		}
		translate.Call(uintptr(unsafe.Pointer(&msg)))
		dispatch.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

var bones = [...][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {0, 5}, {5, 6}, {6, 7}, {7, 8}, {5, 9}, {9, 10}, {10, 11}, {11, 12}, {9, 13}, {13, 14}, {14, 15}, {15, 16}, {13, 17}, {0, 17}, {17, 18}, {18, 19}, {19, 20}}

func drawText(dc uintptr, x, y int, text string) {
	chars, err := syscall.UTF16FromString(text)
	if err != nil {
		return
	}
	gdi.NewProc("TextOutW").Call(dc, uintptr(x), uintptr(y), uintptr(unsafe.Pointer(&chars[0])), uintptr(len(chars)-1))
}

func (p *Window) paint(hwnd uintptr) {
	var ps paintStruct
	dc, _, _ := user.NewProc("BeginPaint").Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	defer user.NewProc("EndPaint").Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	var client rect
	user.NewProc("GetClientRect").Call(hwnd, uintptr(unsafe.Pointer(&client)))
	brush, _, _ := gdi.NewProc("GetStockObject").Call(0)
	user.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&client)), brush)
	font, _, _ := gdi.NewProc("GetStockObject").Call(17)
	oldFont, _, _ := gdi.NewProc("SelectObject").Call(dc, font)
	defer gdi.NewProc("SelectObject").Call(dc, oldFont)
	gdi.NewProc("SetBkMode").Call(dc, 1)
	gdi.NewProc("SetTextColor").Call(dc, 0x00202020)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pixels) == 0 {
		drawText(dc, 16, 16, "等待摄像头画面…")
		return
	}
	imageWidth := int(client.Right)
	imageHeight := imageWidth * p.height / p.width
	help := control.HelpLines()
	if p.hideOnClose {
		help = []string{"F7 打开配置面板：手势、动作和灵敏度均可修改", "当前绑定以配置面板为准；下方显示实时识别与动作", "关闭此预览只会隐藏；F9 或配置面板“退出”结束程序"}
	}
	footerHeight := 240 + len(help)*24
	if imageHeight > int(client.Bottom)-footerHeight {
		imageHeight = max(1, int(client.Bottom)-footerHeight)
		imageWidth = imageHeight * p.width / p.height
	}
	info := bitmapInfo{Size: 40, Width: int32(p.width), Height: -int32(p.height), Planes: 1, Bits: 32}
	gdi.NewProc("StretchDIBits").Call(dc, 0, 0, uintptr(imageWidth), uintptr(imageHeight), 0, 0, uintptr(p.width), uintptr(p.height), uintptr(unsafe.Pointer(&p.pixels[0])), uintptr(unsafe.Pointer(&info)), 0, 0x00CC0020)
	for _, s := range p.states {
		if !s.Present {
			continue
		}
		color := uintptr(0x0000FF00)
		if s.Side == gesture.SideUnknown {
			color = 0x0000FFFF
		}
		pen, _, _ := gdi.NewProc("CreatePen").Call(0, 2, color)
		oldPen, _, _ := gdi.NewProc("SelectObject").Call(dc, pen)
		for _, bone := range bones {
			a, b := s.Landmarks[bone[0]], s.Landmarks[bone[1]]
			gdi.NewProc("MoveToEx").Call(dc, uintptr(int(a.X)*imageWidth/p.width), uintptr(int(a.Y)*imageHeight/p.height), 0)
			gdi.NewProc("LineTo").Call(dc, uintptr(int(b.X)*imageWidth/p.width), uintptr(int(b.Y)*imageHeight/p.height))
		}
		gdi.NewProc("SelectObject").Call(dc, oldPen)
		gdi.NewProc("DeleteObject").Call(pen)
		wrist := s.Landmarks[0]
		gdi.NewProc("SetTextColor").Call(dc, color)
		drawText(dc, max(0, int(wrist.X)*imageWidth/p.width), max(0, int(wrist.Y)*imageHeight/p.height-20), fmt.Sprintf("%s / %s%s", sideName(s.Side), poseName(s.RawPose), facingName(s.Facing)))
	}
	gdi.NewProc("SetTextColor").Call(dc, 0x00202020)
	status := "控制已开启"
	if p.dryRun {
		status = "演示模式（只识别，不发送鼠标输入）"
	}
	if p.paused {
		status = "已暂停（仍显示识别，不发送鼠标输入）"
	}
	drawText(dc, 12, imageHeight+10, fmt.Sprintf("%s    灵敏度可分别调节    左右手交换：%t", status, p.swapped))
	drawText(dc, 12, imageHeight+34, "F6 交换左右手标签    F8 暂停/恢复    F9 或关闭窗口退出")
	for n, line := range help {
		drawText(dc, 12, imageHeight+58+n*24, line)
	}
	var labels []string
	for _, s := range p.states {
		if s.Present {
			label := fmt.Sprintf("%s：%s%s", sideName(s.Side), poseName(s.RawPose), facingName(s.Facing))
			if action := p.running[s.ID]; action != "" {
				label += " → " + action
			}
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		labels = append(labels, "当前未检测到手，请调整摄像头角度或手的位置")
	}
	drawText(dc, 12, imageHeight+58+len(help)*24, strings.Join(labels, "；"))
	drawText(dc, 12, imageHeight+82+len(help)*24, p.lastAction)
}

func sideName(side gesture.Side) string {
	switch side {
	case gesture.SideLeft:
		return "左手"
	case gesture.SideRight:
		return "右手"
	}
	return "左右手不确定"
}
func facingName(facing int8) string {
	switch {
	case facing > 0:
		return " · 掌心"
	case facing < 0:
		return " · 手背"
	}
	return ""
}
func poseName(pose string) string {
	switch pose {
	case "point":
		return "一指"
	case "eight_back":
		return "比八"
	case "two":
		return "两指"
	case "click_pinch":
		return "拇食指捏合"
	case "middle":
		return "只伸中指"
	case "fist":
		return "握拳"
	case "three":
		return "三指"
	case "five":
		return "五指"
	case "four":
		return "四指掌"
	case "rock":
		return "食指+小指"
	case "ok":
		return "OK"
	case "middle_ring_pinky":
		return "后三指"
	case "thumb_up":
		return "拇指向上"
	case "thumb_down":
		return "拇指向下"
	case "thumb_left":
		return "拇指向左"
	case "thumb_right":
		return "拇指向右"
	case "":
		return "未匹配"
	}
	return pose
}
