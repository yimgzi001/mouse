//go:build windows && amd64

// Package wininput implements engine.Injector using native Windows APIs.
package wininput

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"gesturectl/internal/engine"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	sendInput        = user32.NewProc("SendInput")
	getCursorPos     = user32.NewProc("GetCursorPos")
	setCursorPos     = user32.NewProc("SetCursorPos")
	getSystemMetrics = user32.NewProc("GetSystemMetrics")
	getAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	lockWorkStation  = user32.NewProc("LockWorkStation")
	shellExecute     = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
)

type mouseInput struct {
	DX, DY            int32
	Data, Flags, Time uint32
	Extra             uintptr
}

// Mouse is the largest INPUT union member and supplies its native alignment.
type input struct {
	Kind  uint32
	Mouse mouseInput
}
type keyboardInput struct {
	VK, Scan    uint16
	Flags, Time uint32
	Extra       uintptr
}
type point struct{ X, Y int32 }

// Injector latches the first API failure until TakeErr hands it over, so the
// processing loop can let go of input and carry on. It also remembers
// successful presses to retry releases during shutdown.
type Injector struct {
	mu          sync.Mutex
	firstErr    error
	heldButtons map[string]bool
	heldKeys    map[uint16]bool
	send        func([]input) error
	dry         bool
	output      io.Writer
	x, y        int
}

var _ engine.Injector = (*Injector)(nil)

func New() *Injector {
	return &Injector{heldButtons: map[string]bool{}, heldKeys: map[uint16]bool{}, send: sendNative}
}

func NewDryRun(output io.Writer) *Injector {
	i := New()
	i.x, i.y = i.CursorPos()
	i.dry = true
	i.output = output
	return i
}

func sendNative(in []input) error {
	if len(in) == 0 {
		return nil
	}
	n, _, err := sendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(input{}))
	runtime.KeepAlive(in)
	if n != uintptr(len(in)) {
		return fmt.Errorf("SendInput accepted %d/%d events (Windows may block input into an elevated app): %w", n, len(in), nativeError(err))
	}
	return nil
}

func nativeError(err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return errors.New("Windows API returned failure without an error code")
	}
	return err
}

func (i *Injector) remember(err error) {
	if err != nil && i.firstErr == nil {
		i.firstErr = err
	}
}
func (i *Injector) submit(in []input) error {
	if i.dry {
		for _, p := range in {
			if _, err := fmt.Fprintf(i.output, "input type=%d flags=0x%x data=%d\n", p.Kind, p.Mouse.Flags, p.Mouse.Data); err != nil {
				return err
			}
		}
		return nil
	}
	return i.send(in)
}
func (i *Injector) Err() error { i.mu.Lock(); defer i.mu.Unlock(); return i.firstErr }

// TakeErr returns the first failure since the last call and forgets it.
func (i *Injector) TakeErr() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	err := i.firstErr
	i.firstErr = nil
	return err
}

func (i *Injector) CursorPos() (int, int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.dry {
		return i.x, i.y
	}
	var p point
	ok, _, err := getCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if ok == 0 {
		i.remember(fmt.Errorf("GetCursorPos: %w", nativeError(err)))
	}
	return int(p.X), int(p.Y)
}
func (i *Injector) MoveCursor(x, y int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.dry {
		i.x, i.y = x, y
		_, err := fmt.Fprintf(i.output, "move %d %d\n", x, y)
		i.remember(err)
		return
	}
	ok, _, err := setCursorPos.Call(uintptr(x), uintptr(y))
	if ok == 0 {
		i.remember(fmt.Errorf("SetCursorPos: %w", nativeError(err)))
	}
}
func (*Injector) Desktop() (x, y, w, h, primaryHeight int) {
	metric := func(n uintptr) int { v, _, _ := getSystemMetrics.Call(n); return int(int32(v)) }
	return metric(76), metric(77), metric(78), metric(79), metric(1)
}

func buttonPacket(button string, down bool) (input, error) {
	p := input{}
	switch button {
	case "left":
		p.Mouse.Flags = 0x0002
	case "right":
		p.Mouse.Flags = 0x0008
	case "middle":
		p.Mouse.Flags = 0x0020
	case "x1":
		p.Mouse.Flags = 0x0080
		p.Mouse.Data = 1
	case "x2":
		p.Mouse.Flags = 0x0080
		p.Mouse.Data = 2
	default:
		return p, fmt.Errorf("unknown mouse button %q", button)
	}
	if !down {
		p.Mouse.Flags <<= 1
	}
	return p, nil
}
func (i *Injector) MouseButton(button string, down bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	p, err := buttonPacket(button, down)
	if err == nil {
		err = i.submit([]input{p})
	}
	i.remember(err)
	if err == nil {
		if down {
			i.heldButtons[button] = true
		} else {
			delete(i.heldButtons, button)
		}
	}
}
func (i *Injector) Wheel(delta int, horizontal bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	p := input{}
	p.Mouse.Flags = 0x0800
	p.Mouse.Data = uint32(int32(delta))
	if horizontal {
		p.Mouse.Flags = 0x1000
	}
	i.remember(i.submit([]input{p}))
}
func keyPacket(vk, scan uint16, flags uint32) input {
	p := input{Kind: 1}
	k := (*keyboardInput)(unsafe.Pointer(&p.Mouse))
	k.VK = vk
	k.Scan = scan
	k.Flags = flags
	return p
}
func virtualKeyPacket(vk uint16, down bool) input {
	flags := uint32(0)
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x2D, 0x2E, 0x5B, 0x5C, 0x6F, 0x90, 0xA3, 0xA5:
		flags |= 1
	}
	if !down {
		flags |= 2
	}
	return keyPacket(vk, 0, flags)
}
func (i *Injector) Key(vk uint16, down bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	err := i.submit([]input{virtualKeyPacket(vk, down)})
	i.remember(err)
	if err == nil {
		if down {
			i.heldKeys[vk] = true
		} else {
			delete(i.heldKeys, vk)
		}
	}
}
func (i *Injector) Text(text string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, u := range utf16.Encode([]rune(text)) {
		if err := i.submit([]input{keyPacket(0, u, 4), keyPacket(0, u, 6)}); err != nil {
			i.remember(err)
			return
		}
	}
}
func (i *Injector) LockScreen() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.dry {
		_, err := fmt.Fprintln(i.output, "lock screen")
		i.remember(err)
		return
	}
	ok, _, err := lockWorkStation.Call()
	if ok == 0 {
		i.remember(fmt.Errorf("LockWorkStation: %w", nativeError(err)))
	}
}
func (i *Injector) Open(target string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.dry {
		_, err := fmt.Fprintf(i.output, "open %q\n", target)
		return err
	}
	path, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, _ := shellExecute.Call(0, 0, uintptr(unsafe.Pointer(path)), 0, 0, 1)
	if result <= 32 {
		return fmt.Errorf("ShellExecuteW returned %d", result)
	}
	return nil
}

// Close releases only buttons and keys successfully pressed by this injector.
func (i *Injector) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for button := range i.heldButtons {
		p, _ := buttonPacket(button, false)
		err := i.submit([]input{p})
		i.remember(err)
		if err == nil {
			delete(i.heldButtons, button)
		}
	}
	for vk := range i.heldKeys {
		err := i.submit([]input{virtualKeyPacket(vk, false)})
		i.remember(err)
		if err == nil {
			delete(i.heldKeys, vk)
		}
	}
	return i.firstErr
}

// KeyPressed reads the down bit, rather than the unreliable "since last call" bit.
func KeyPressed(vk uint16) bool {
	state, _, _ := getAsyncKeyState.Call(uintptr(vk))
	return state&0x8000 != 0
}

// EnableDPI keeps desktop bounds and cursor coordinates in physical pixels.
func EnableDPI() error {
	proc := user32.NewProc("SetProcessDpiAwarenessContext")
	if err := proc.Find(); err != nil {
		ok, _, callErr := user32.NewProc("SetProcessDPIAware").Call()
		if ok == 0 {
			return fmt.Errorf("enable DPI awareness: %w", nativeError(callErr))
		}
		return nil
	}
	ok, _, err := proc.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	if ok == 0 && !errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("enable DPI awareness: %w", nativeError(err))
	}
	return nil
}
