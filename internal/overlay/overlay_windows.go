//go:build windows

package overlay

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"gesturectl/internal/control"
	"gesturectl/internal/engine"
	"gesturectl/internal/winui"
)

var (
	user   = syscall.NewLazyDLL("user32.dll")
	gdi    = syscall.NewLazyDLL("gdi32.dll")
	kernel = syscall.NewLazyDLL("kernel32.dll")
	shcore = syscall.NewLazyDLL("shcore.dll")

	registerClass       = user.NewProc("RegisterClassExW")
	createWindow        = user.NewProc("CreateWindowExW")
	defWindowProc       = user.NewProc("DefWindowProcW")
	getMessage          = user.NewProc("GetMessageW")
	translateMessage    = user.NewProc("TranslateMessage")
	dispatchMessage     = user.NewProc("DispatchMessageW")
	postMessage         = user.NewProc("PostMessageW")
	postQuitMessage     = user.NewProc("PostQuitMessage")
	setTimer            = user.NewProc("SetTimer")
	showWindow          = user.NewProc("ShowWindow")
	setWindowPos        = user.NewProc("SetWindowPos")
	updateLayered       = user.NewProc("UpdateLayeredWindow")
	getCursorPos        = user.NewProc("GetCursorPos")
	getDC               = user.NewProc("GetDC")
	releaseDC           = user.NewProc("ReleaseDC")
	monitorFromPoint    = user.NewProc("MonitorFromPoint")
	getMonitorInfo      = user.NewProc("GetMonitorInfoW")
	threadDPIAwareness  = user.NewProc("GetThreadDpiAwarenessContext")
	awarenessOfContext  = user.NewProc("GetAwarenessFromDpiAwarenessContext")
	getDpiForMonitor    = shcore.NewProc("GetDpiForMonitor")
	getModuleHandle     = kernel.NewProc("GetModuleHandleW")
	createCompatibleDC  = gdi.NewProc("CreateCompatibleDC")
	deleteDC            = gdi.NewProc("DeleteDC")
	selectObject        = gdi.NewProc("SelectObject")
	deleteObject        = gdi.NewProc("DeleteObject")
	className, classErr = syscall.UTF16PtrFromString("GestureMouseCursorHint")
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmMouseActivate = 0x0021
	wmNCHitTest     = 0x0084
	wmTimer         = 0x0113
	wmWake          = 0x8001

	// Layered, click-through, topmost, out of the task bar and never active.
	exStyle = 0x80000 | 0x20 | 0x8 | 0x80 | 0x08000000

	// The hint follows the cursor at the timer's pace while it shows, and
	// looks for work less often while it hides.
	followTick = 16
	idleTick   = 100
)

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type monitorInfo struct {
	Size          uint32
	Monitor, Work rect
	Flags         uint32
}

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	MenuName, ClassName                *uint16
	SmallIcon                          uintptr
}

type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}

var (
	registration sync.Once
	registerErr  error
	// overlays maps window handles to their Overlay for the window procedure.
	overlays sync.Map
)

// Overlay is a window that floats by the cursor, lets clicks through and never
// takes the focus. Its methods may be called from any goroutine; on a nil
// Overlay they do nothing.
type Overlay struct {
	mu    sync.Mutex
	state State
	shown bool

	hwnd   uintptr
	done   chan struct{}
	closed sync.Once

	// The window's thread alone uses these.
	memDC         uintptr
	bitmap        uintptr
	width, height int
	key           string
	fonts         map[int]uintptr
}

// New opens the hint window, hidden until there is something to show.
func New() (*Overlay, error) {
	o := &Overlay{done: make(chan struct{}), fonts: map[int]uintptr{}}
	ready := make(chan error, 1)
	go o.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return o, nil
}

func register() error {
	registration.Do(func() {
		if classErr != nil {
			registerErr = classErr
			return
		}
		instance, _, _ := getModuleHandle.Call(0)
		wc := windowClass{Proc: syscall.NewCallback(windowProc), Instance: instance, ClassName: className}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
			registerErr = fmt.Errorf("RegisterClassExW: %w", err)
		}
	})
	return registerErr
}

func (o *Overlay) run(ready chan<- error) {
	// The window belongs to this thread, which must pump its messages.
	runtime.LockOSThread()
	defer close(o.done)
	if err := register(); err != nil {
		ready <- err
		return
	}
	instance, _, _ := getModuleHandle.Call(0)
	hwnd, _, err := createWindow.Call(exStyle, uintptr(unsafe.Pointer(className)), 0, 0x80000000, 0, 0, 1, 1, 0, 0, instance, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowExW: %w", err)
		return
	}
	o.hwnd = hwnd
	overlays.Store(hwnd, o)
	defer overlays.Delete(hwnd)
	o.memDC, _, _ = createCompatibleDC.Call(0)
	defer o.release()
	setTimer.Call(hwnd, 1, idleTick, 0)
	ready <- nil
	var m message
	for {
		if r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0); int32(r) <= 0 {
			return
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (o *Overlay) release() {
	if o.memDC != 0 {
		deleteDC.Call(o.memDC)
	}
	if o.bitmap != 0 {
		deleteObject.Call(o.bitmap)
	}
	for _, font := range o.fonts {
		deleteObject.Call(font)
	}
}

func windowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmNCHitTest:
		return ^uintptr(0) // HTTRANSPARENT: the click goes to the window below.
	case wmMouseActivate:
		return 3 // MA_NOACTIVATE
	case wmTimer, wmWake:
		if o, ok := overlays.Load(hwnd); ok {
			o.(*Overlay).tick()
		}
		return 0
	case wmDestroy:
		postQuitMessage.Call(0)
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, msg, wParam, lParam)
	return r
}

// SetEnabled shows or leaves out the hands; the pause notice shows either way.
func (o *Overlay) SetEnabled(on bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.state.Hidden = !on
	o.mu.Unlock()
}

// Frame passes the hints and the engine events of a frame.
func (o *Overlay) Frame(hints []control.Hint, events []engine.Event) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.state.Frame(hints, events, time.Now())
	wake := !o.shown && !o.state.Hidden && (len(hints) > 0 || len(events) > 0)
	o.mu.Unlock()
	if wake {
		postMessage.Call(o.hwnd, wmWake, 0, 0)
	}
}

// Paused shows for a moment that the control was paused or resumed.
func (o *Overlay) Paused(paused bool) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.state.Paused(paused, time.Now())
	o.mu.Unlock()
	postMessage.Call(o.hwnd, wmWake, 0, 0)
}

// Close destroys the window and waits for its thread to end.
func (o *Overlay) Close() {
	if o == nil {
		return
	}
	o.closed.Do(func() {
		postMessage.Call(o.hwnd, wmClose, 0, 0)
		<-o.done
	})
}

func (o *Overlay) tick() {
	o.mu.Lock()
	rows := o.state.Rows(time.Now())
	o.mu.Unlock()
	if len(rows) == 0 {
		o.show(false)
		return
	}
	var cursor point
	getCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	monitor, _, _ := monitorFromPoint.Call(uintptr(uint32(cursor.X))|uintptr(uint32(cursor.Y))<<32, 2)
	dpi := o.dpi(monitor)
	if key := sceneKey(rows, dpi); key != o.key || o.bitmap == 0 {
		canvas := Draw(rows, dpi, o.text(dpi))
		bitmap := winui.Bitmap(canvas)
		if bitmap == 0 {
			return
		}
		selectObject.Call(o.memDC, bitmap)
		if o.bitmap != 0 {
			deleteObject.Call(o.bitmap)
		}
		o.bitmap, o.width, o.height, o.key = bitmap, canvas.W, canvas.H, key
	}
	work := Rect{-1 << 30, -1 << 30, 1 << 30, 1 << 30}
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
		work = Rect{int(info.Work.Left), int(info.Work.Top), int(info.Work.Right), int(info.Work.Bottom)}
	}
	at := Place(Point{int(cursor.X), int(cursor.Y)}, o.width, o.height, work, dpi)
	dst, src := point{int32(at.X), int32(at.Y)}, point{}
	size := struct{ CX, CY int32 }{int32(o.width), int32(o.height)}
	blend := uint32(0x01ff0000) // AC_SRC_OVER, constant alpha 255, AC_SRC_ALPHA
	screen, _, _ := getDC.Call(0)
	updateLayered.Call(o.hwnd, screen, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&size)), o.memDC, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), 2)
	releaseDC.Call(0, screen)
	o.show(true)
}

func (o *Overlay) show(on bool) {
	o.mu.Lock()
	changed := o.shown != on
	o.shown = on
	o.mu.Unlock()
	if !changed {
		return
	}
	if on {
		showWindow.Call(o.hwnd, 4) // SW_SHOWNOACTIVATE
		setWindowPos.Call(o.hwnd, ^uintptr(0), 0, 0, 0, 0, 0x13)
		setTimer.Call(o.hwnd, 1, followTick, 0)
	} else {
		showWindow.Call(o.hwnd, 0)
		setTimer.Call(o.hwnd, 1, idleTick, 0)
	}
}

// dpi is the scaling of the monitor under the cursor when the program sees
// each monitor's own, and else the one Windows draws the window at.
func (o *Overlay) dpi(monitor uintptr) int {
	if threadDPIAwareness.Find() == nil && awarenessOfContext.Find() == nil && getDpiForMonitor.Find() == nil {
		context, _, _ := threadDPIAwareness.Call()
		if awareness, _, _ := awarenessOfContext.Call(context); awareness == 2 {
			var x, y uint32
			if hr, _, _ := getDpiForMonitor.Call(monitor, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); hr == 0 && x != 0 {
				return int(x)
			}
		}
	}
	return winui.DPI(o.hwnd)
}

func (o *Overlay) text(dpi int) TextFunc {
	font, ok := o.fonts[dpi]
	if !ok {
		font = winui.Font(10, dpi, 600, winui.Grey, "Microsoft YaHei UI")
		o.fonts[dpi] = font
	}
	return func(s string) ([]byte, int, int) { return winui.TextMask(font, s) }
}

// sceneKey changes when the rows would draw differently.
func sceneKey(rows []Row, dpi int) string {
	var b strings.Builder
	fmt.Fprint(&b, dpi)
	for _, r := range rows {
		fmt.Fprintf(&b, "|%d %q %v %t %d %d", r.Look, r.Text, r.Hand, r.HasHand, int(r.Progress*60), int(r.Glow*15))
	}
	return b.String()
}
