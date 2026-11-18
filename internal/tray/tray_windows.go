//go:build windows

// Package tray puts the program in the notification area: an icon that turns
// grey while gestures are paused, a double click that opens the settings, and
// a menu to pause or resume, start with Windows and exit.
package tray

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gesturectl/internal/winui"
)

var (
	user   = syscall.NewLazyDLL("user32.dll")
	gdi    = syscall.NewLazyDLL("gdi32.dll")
	shell  = syscall.NewLazyDLL("shell32.dll")
	kernel = syscall.NewLazyDLL("kernel32.dll")

	allowForeground    = user.NewProc("AllowSetForegroundWindow")
	appendMenu         = user.NewProc("AppendMenuW")
	createIconIndirect = user.NewProc("CreateIconIndirect")
	createPopupMenu    = user.NewProc("CreatePopupMenu")
	createWindow       = user.NewProc("CreateWindowExW")
	defWindowProc      = user.NewProc("DefWindowProcW")
	destroyIcon        = user.NewProc("DestroyIcon")
	destroyMenu        = user.NewProc("DestroyMenu")
	destroyWindow      = user.NewProc("DestroyWindow")
	dispatchMessage    = user.NewProc("DispatchMessageW")
	findWindow         = user.NewProc("FindWindowW")
	getCursorPos       = user.NewProc("GetCursorPos")
	getMessage         = user.NewProc("GetMessageW")
	getSystemMetrics   = user.NewProc("GetSystemMetrics")
	postMessage        = user.NewProc("PostMessageW")
	postQuitMessage    = user.NewProc("PostQuitMessage")
	registerClass      = user.NewProc("RegisterClassExW")
	registerMessage    = user.NewProc("RegisterWindowMessageW")
	setForeground      = user.NewProc("SetForegroundWindow")
	setMenuDefaultItem = user.NewProc("SetMenuDefaultItem")
	setTimer           = user.NewProc("SetTimer")
	trackPopupMenu     = user.NewProc("TrackPopupMenu")
	translateMessage   = user.NewProc("TranslateMessage")
	windowProcess      = user.NewProc("GetWindowThreadProcessId")
	createBitmap       = gdi.NewProc("CreateBitmap")
	deleteObject       = gdi.NewProc("DeleteObject")
	notifyIcon         = shell.NewProc("Shell_NotifyIconW")
	moduleHandle       = kernel.NewProc("GetModuleHandleW")
)

// className names the hidden window behind the icon, so that another copy of
// the program can find it.
const className = "GestureMouseTray"

// Messages to the hidden window.
const (
	msgNotify = 0x8001 // from the icon; lParam is the mouse message
	msgOpen   = 0x8002 // from another copy of the program
)

const (
	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmContextMenu   = 0x007b
	wmTimer         = 0x0113
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
)

const (
	nimAdd, nimModify, nimDelete = 0, 1, 2
	nifMessage, nifIcon, nifTip  = 1, 2, 4
)

type windowClass struct {
	Size, Style        uint32
	WndProc            uintptr
	ClsExtra, WndExtra int32
	Instance, Icon     uintptr
	Cursor, Background uintptr
	MenuName           *uint16
	ClassName          *uint16
	IconSm             uintptr
}

type notifyIconData struct {
	Size             uint32
	Wnd              uintptr
	ID, Flags        uint32
	CallbackMessage  uint32
	Icon             uintptr
	Tip              [128]uint16
	State, StateMask uint32
	Info             [256]uint16
	Version          uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUID             [16]byte
	BalloonIcon      uintptr
}

type iconInfo struct {
	Icon        int32
	X, Y        uint32
	Mask, Color uintptr
}

type point struct{ X, Y int32 }

type message struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

// Options connect the icon to the program. Open may be nil when there are no
// settings to open, and Startup nil to leave starting with Windows out of the
// menu. OnError hears about failures to change the startup entry.
type Options struct {
	Paused    func() bool
	SetPaused func(bool)
	Open      func()
	Exit      func()
	Startup   *Startup
	OnError   func(error)
}

// Tray is the program's icon in the notification area. Its window lives on a
// thread of its own, which also runs the menu and the callbacks.
type Tray struct {
	options        Options
	hwnd           uintptr
	icons          [2]uintptr
	taskbarCreated uint32
	added          bool
	// paused is the state the icon shows.
	paused  atomic.Bool
	done    chan struct{}
	closing sync.Once
}

var (
	registration struct {
		once sync.Once
		err  error
	}
	// trays finds the Tray of a window for the shared window procedure.
	trays sync.Map
)

func utf16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// New shows the icon. It fails when the window behind it cannot be made; a
// taskbar that is not there yet, as early in a sign-in, only delays the icon.
func New(options Options) (*Tray, error) {
	if options.Paused == nil || options.SetPaused == nil || options.Exit == nil {
		return nil, errors.New("通知区域图标需要暂停和退出回调")
	}
	t := &Tray{options: options, done: make(chan struct{})}
	ready := make(chan error, 1)
	go t.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return t, nil
}

// Close takes the icon away. It is safe on a nil Tray and more than once.
func (t *Tray) Close() {
	if t == nil {
		return
	}
	t.closing.Do(func() {
		postMessage.Call(t.hwnd, wmClose, 0, 0)
		<-t.done
	})
}

// ShowRunning asks a copy of the program that already runs to open its
// settings, and reports whether it found one.
func ShowRunning() bool {
	hwnd, _, _ := findWindow.Call(uintptr(unsafe.Pointer(utf16(className))), 0)
	if hwnd == 0 {
		return false
	}
	// The copy the user just started may bring its own window forward; let
	// the running one do so instead.
	var process uint32
	windowProcess.Call(hwnd, uintptr(unsafe.Pointer(&process)))
	allowForeground.Call(uintptr(process))
	posted, _, _ := postMessage.Call(hwnd, msgOpen, 0, 0)
	return posted != 0
}

func register(instance uintptr) error {
	registration.once.Do(func() {
		proc := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
			if t, ok := trays.Load(hwnd); ok && t.(*Tray).handle(msg, lp) {
				return 0
			}
			r, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wp, lp)
			return r
		})
		class := windowClass{WndProc: proc, Instance: instance, ClassName: utf16(className)}
		class.Size = uint32(unsafe.Sizeof(class))
		if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
			registration.err = fmt.Errorf("注册通知区域窗口：%w", err)
		}
	})
	return registration.err
}

func (t *Tray) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)
	instance, _, _ := moduleHandle.Call(0)
	if err := register(instance); err != nil {
		ready <- err
		return
	}
	size := 16
	if n, _, _ := getSystemMetrics.Call(49); int(n) > size {
		size = int(n)
	}
	t.icons = [2]uintptr{makeIcon(drawIcon(size, false)), makeIcon(drawIcon(size, true))}
	defer func() {
		for _, icon := range t.icons {
			if icon != 0 {
				destroyIcon.Call(icon)
			}
		}
	}()
	if t.icons[0] == 0 || t.icons[1] == 0 {
		ready <- errors.New("画不出通知区域图标")
		return
	}
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(utf16(className))), uintptr(unsafe.Pointer(utf16("手势鼠标"))), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("创建通知区域窗口：%w", err)
		return
	}
	t.hwnd = hwnd
	trays.Store(hwnd, t)
	defer trays.Delete(hwnd)
	name := utf16("TaskbarCreated")
	created, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(name)))
	t.taskbarCreated = uint32(created)
	t.add()
	setTimer.Call(hwnd, 1, 500, 0)
	ready <- nil
	var msg message
	for {
		if r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0); int32(r) <= 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// handle runs on the window thread and reports whether it took the message.
func (t *Tray) handle(msg uint32, lp uintptr) bool {
	switch {
	case msg == msgNotify:
		switch uint32(lp) {
		case wmLButtonDblClk:
			t.open()
		case wmRButtonUp, wmContextMenu:
			t.menu()
		}
	case msg == msgOpen:
		t.open()
	case msg == wmTimer:
		t.refresh()
	case msg == t.taskbarCreated && msg != 0:
		// Explorer started again and has forgotten the icon.
		t.added = false
		t.add()
	case msg == wmClose:
		if t.added {
			data := t.data()
			notifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
			t.added = false
		}
		destroyWindow.Call(t.hwnd)
	case msg == wmDestroy:
		postQuitMessage.Call(0)
	default:
		return false
	}
	return true
}

func (t *Tray) data() notifyIconData {
	d := notifyIconData{Wnd: t.hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: msgNotify}
	d.Size = uint32(unsafe.Sizeof(d))
	paused := t.paused.Load()
	d.Icon = t.icons[0]
	if paused {
		d.Icon = t.icons[1]
	}
	text, _ := syscall.UTF16FromString(tip(paused, t.options.Open != nil))
	copy(d.Tip[:len(d.Tip)-1], text)
	return d
}

// add puts the icon in the notification area, unless the taskbar is not there
// yet; the timer tries again.
func (t *Tray) add() {
	t.paused.Store(t.options.Paused())
	data := t.data()
	if r, _, _ := notifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data))); r == 0 {
		// The taskbar may have been rebuilt without losing its icons.
		if r, _, _ = notifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&data))); r == 0 {
			return
		}
	}
	t.added = true
}

// refresh follows a pause or resume made elsewhere: a hotkey or the panel.
func (t *Tray) refresh() {
	if !t.added {
		t.add()
		return
	}
	if paused := t.options.Paused(); paused != t.paused.Load() {
		t.paused.Store(paused)
		data := t.data()
		notifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&data)))
	}
}

func (t *Tray) open() {
	if t.options.Open != nil {
		t.options.Open()
	}
}

func (t *Tray) fail(err error) {
	if err != nil && t.options.OnError != nil {
		t.options.OnError(err)
	}
}

func (t *Tray) menu() {
	paused := t.options.Paused()
	starts := false
	if t.options.Startup != nil {
		var err error
		starts, err = t.options.Startup.Enabled()
		t.fail(err)
	}
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	const mfChecked, mfSeparator = 0x8, 0x800
	for _, item := range menuItems(t.options.Open != nil, paused, t.options.Startup != nil, starts) {
		if item.id == 0 {
			appendMenu.Call(menu, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(0)
		if item.checked {
			flags |= mfChecked
		}
		appendMenu.Call(menu, flags, uintptr(item.id), uintptr(unsafe.Pointer(utf16(item.text))))
	}
	if t.options.Open != nil {
		setMenuDefaultItem.Call(menu, itemOpen, 0)
	}
	var at point
	getCursorPos.Call(uintptr(unsafe.Pointer(&at)))
	// Without the foreground the menu would stay open after a click elsewhere,
	// and without the null message a second right click would not reopen it.
	setForeground.Call(t.hwnd)
	const tpmRightButton, tpmNoNotify, tpmReturnCmd = 0x2, 0x80, 0x100
	chosen, _, _ := trackPopupMenu.Call(menu, tpmRightButton|tpmNoNotify|tpmReturnCmd, uintptr(at.X), uintptr(at.Y), 0, t.hwnd, 0)
	postMessage.Call(t.hwnd, wmNull, 0, 0)
	switch chosen {
	case itemOpen:
		t.open()
	case itemPause:
		t.options.SetPaused(!paused)
		t.refresh()
	case itemStartup:
		t.fail(t.options.Startup.Set(!starts))
	case itemExit:
		t.options.Exit()
	}
}

// makeIcon turns a canvas into an icon. Icons take colours that are not
// premultiplied by their alpha, unlike the canvas.
func makeIcon(c *winui.Canvas) uintptr {
	straight := &winui.Canvas{W: c.W, H: c.H, Pix: make([]byte, len(c.Pix))}
	for i := 0; i+3 < len(c.Pix); i += 4 {
		a := int(c.Pix[i+3])
		if a == 0 {
			continue
		}
		for k := range 3 {
			straight.Pix[i+k] = uint8(min(255, (int(c.Pix[i+k])*255+a/2)/a))
		}
		straight.Pix[i+3] = uint8(a)
	}
	color := winui.Bitmap(straight)
	if color == 0 {
		return 0
	}
	defer deleteObject.Call(color)
	// The mask is required but the alpha channel overrides it.
	bits := make([]byte, (c.W+15)/16*2*c.H)
	mask, _, _ := createBitmap.Call(uintptr(c.W), uintptr(c.H), 1, 1, uintptr(unsafe.Pointer(&bits[0])))
	if mask == 0 {
		return 0
	}
	defer deleteObject.Call(mask)
	info := iconInfo{Icon: 1, Mask: mask, Color: color}
	icon, _, _ := createIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	return icon
}
