//go:build windows && amd64

package wininput

import (
	"encoding/json"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gesturectl/internal/control"
	"gesturectl/internal/vision"
)

// Opt-in: creates a temporary test window and confines all clicks to it.
// Ordinary go test never moves the real pointer or clicks other applications.
func TestLiveDesktopPointerAndClicks(t *testing.T) {
	if os.Getenv("MOUSE_LIVE_TEST") != "1" {
		t.Skip("set MOUSE_LIVE_TEST=1 to test the real desktop")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := EnableDPI(); err != nil {
		t.Fatal(err)
	}
	inj := New()
	var sentPackets []input
	inj.send = func(packets []input) error { sentPackets = append(sentPackets, packets...); return sendNative(packets) }
	oldX, oldY := inj.CursorPos()
	defer func() { inj.Close(); inj.MoveCursor(oldX, oldY) }()

	type windowClass struct {
		Style                              uint32
		Proc                               uintptr
		ClassExtra, WindowExtra            int32
		Instance, Icon, Cursor, Background uintptr
		Menu, Name                         *uint16
	}
	type message struct {
		Window  uintptr
		ID      uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      point
		Private uint32
	}
	var received []uint32
	defWindow := user32.NewProc("DefWindowProcW")
	callback := syscall.NewCallback(func(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
		switch msg {
		case 0x0201, 0x0202, 0x0204, 0x0205:
			received = append(received, msg)
			return 0
		case 0x0021:
			return 3 // MA_NOACTIVATE
		}
		r, _, _ := defWindow.Call(hwnd, uintptr(msg), wparam, lparam)
		return r
	})
	name, _ := syscall.UTF16PtrFromString("GestureMouseInputTest")
	instance, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	wc := windowClass{Proc: callback, Instance: instance, Name: name}
	atom, _, err := user32.NewProc("RegisterClassW").Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		t.Fatalf("RegisterClass: %v", err)
	}
	defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	hwnd, _, err := user32.NewProc("CreateWindowExW").Call(0x08000008, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0x90000000, 100, 100, 600, 400, 0, 0, instance, 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindow: %v", err)
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)
	target := point{X: 300, Y: 200}
	ok, _, err := user32.NewProc("ClientToScreen").Call(hwnd, uintptr(unsafe.Pointer(&target)))
	if ok == 0 {
		t.Fatal(err)
	}
	inj.MoveCursor(int(target.X), int(target.Y))
	x, y := inj.CursorPos()
	if x != int(target.X) || y != int(target.Y) {
		t.Fatalf("cursor got %d,%d want %d,%d", x, y, target.X, target.Y)
	}
	under, _, _ := user32.NewProc("WindowFromPoint").Call(uintptr(uint64(uint32(target.X)) | uint64(uint32(target.Y))<<32))
	if under != hwnd {
		t.Fatal("test window is not under cursor; refusing to click")
	}
	data, err := os.ReadFile("../../testdata/reference/control_hands.json")
	if err != nil {
		t.Fatal(err)
	}
	var hands map[string][]vision.Hand
	if err = json.Unmarshal(data, &hands); err != nil {
		t.Fatal(err)
	}
	ctl, err := control.New(inj, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Abort()
	now := time.Now()
	feed := func(h vision.Hand, frames int) {
		h.TrackID = 1
		for n := 0; n < frames; n++ {
			ctl.Process([]vision.Hand{h}, now)
			now = now.Add(33 * time.Millisecond)
		}
	}
	pointing := hands["pointing_up.jpg"][0]
	pointing.LeftScore = .99
	feed(pointing, 12)
	for n := 0; n < 6; n++ {
		for j := range pointing.Landmarks {
			pointing.Landmarks[j].X += 1
		}
		feed(pointing, 1)
	}
	movedX, movedY := inj.CursorPos()
	if movedX <= x || movedY != y {
		t.Fatalf("landmarks did not move native pointer right: %d,%d -> %d,%d", x, y, movedX, movedY)
	}
	under, _, _ = user32.NewProc("WindowFromPoint").Call(uintptr(uint64(uint32(movedX)) | uint64(uint32(movedY))<<32))
	if under != hwnd {
		t.Fatal("pointer left test window; refusing to click")
	}
	click := hands["fist.jpg"][0]
	click.LeftScore = .99
	feed(click, 30) // held gesture must produce one click
	ctl.Abort()
	click = hands["victory.jpg"][0]
	click.LeftScore = .99
	feed(click, 30)
	if err := inj.Err(); err != nil {
		t.Fatal(err)
	}
	peek := user32.NewProc("PeekMessageW")
	dispatch := user32.NewProc("DispatchMessageW")
	deadline := time.Now().Add(time.Second)
	for len(received) < 4 && time.Now().Before(deadline) {
		var msg message
		for {
			have, _, _ := peek.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
			if have == 0 {
				break
			}
			dispatch.Call(uintptr(unsafe.Pointer(&msg)))
		}
		time.Sleep(time.Millisecond)
	}
	want := []uint32{0x0201, 0x0202, 0x0204, 0x0205}
	if len(received) != len(want) {
		_, events := ctl.Feedback()
		cx, cy := inj.CursorPos()
		t.Logf("events=%+v packets=%+v cursor=%d,%d", events, sentPackets, cx, cy)
		t.Fatalf("native button messages: %x", received)
	}
	for n := range want {
		if received[n] != want[n] {
			t.Fatalf("native messages: %x", received)
		}
	}
	t.Logf("native cursor %d,%d -> %d,%d; window received one left click and one right click", x, y, movedX, movedY)
}
