//go:build windows

package tray

import (
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// This test shows a real icon in the notification area for a moment.
func TestTrayOpensFollowsPauseAndLeaves(t *testing.T) {
	var paused atomic.Bool
	opened := make(chan struct{}, 2)
	tr, err := New(Options{Paused: paused.Load, SetPaused: paused.Store, Open: func() { opened <- struct{}{} }, Exit: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	wait := func(what string) {
		t.Helper()
		select {
		case <-opened:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not open the settings", what)
		}
	}
	postMessage.Call(tr.hwnd, msgNotify, 1, wmLButtonDblClk)
	wait("a double click on the icon")
	if !ShowRunning() {
		t.Fatal("another copy did not find the icon")
	}
	wait("another copy")
	paused.Store(true)
	for deadline := time.Now().Add(2 * time.Second); !tr.paused.Load(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the icon did not turn grey after a pause made elsewhere")
		}
	}
	tr.Close()
	if hwnd, _, _ := findWindow.Call(uintptr(unsafe.Pointer(utf16(className))), 0); hwnd != 0 {
		t.Fatal("the window behind the icon outlived Close")
	}
}
