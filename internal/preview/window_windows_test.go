//go:build windows && amd64

package preview

import (
	"gesturectl/internal/control"
	"gesturectl/internal/vision"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestLivePreviewCopiesFrameAndCloses(t *testing.T) {
	if os.Getenv("MOUSE_PREVIEW_TEST") != "1" {
		t.Skip("set MOUSE_PREVIEW_TEST=1 to test the native preview window")
	}
	var closed atomic.Int32
	p, err := New(func() { closed.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	frame := &vision.Frame{W: 2, H: 2, Pix: []byte{255, 0, 0, 0, 255, 0, 0, 0, 255, 255, 255, 255}}
	p.Publish(frame, nil, true, false, .5, false)
	frame.Pix[0] = 0 // capture will reuse the source buffer immediately
	p.mu.Lock()
	red := p.pixels[2]
	p.mu.Unlock()
	if red != 255 {
		t.Error("preview retained the borrowed capture buffer")
	}
	time.Sleep(50 * time.Millisecond)
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("preview Close hung")
	}
	if closed.Load() != 1 {
		t.Fatalf("close callback ran %d times", closed.Load())
	}
	p.Close()
}

func TestNativeSensitivitySlidersSaveIndependentValues(t *testing.T) {
	if os.Getenv("MOUSE_PREVIEW_TEST") != "1" {
		t.Skip("set MOUSE_PREVIEW_TEST=1")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	initial := control.DefaultPreferences()
	applied := make(chan control.Preferences, 1)
	p, err := NewWithPreferences(nil, initial, func(next control.Preferences) error {
		if err := control.SavePreferences(path, next); err != nil {
			return err
		}
		applied <- next
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	slider := p.sliders[0].track
	user.NewProc("SendMessageW").Call(slider, 0x0405, 1, 75)
	post.Call(p.hwnd, 0x0114, 8, slider)
	select {
	case got := <-applied:
		if got.MoveLeft != .75 || got.MoveRight != .5 || got.Switcher != 2.5 {
			t.Fatalf("settings changed other fields: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slider did not apply")
	}
	stored, err := control.LoadPreferences(path)
	if err != nil || stored.MoveLeft != .75 {
		t.Fatalf("not persisted: %+v %v", stored, err)
	}
}

func TestControlledPreviewHidesWithoutExitingAndSkipsHiddenPixels(t *testing.T) {
	if os.Getenv("MOUSE_PREVIEW_TEST") != "1" {
		t.Skip("set MOUSE_PREVIEW_TEST=1")
	}
	var closed atomic.Int32
	p, err := NewWithOptions(Options{Preferences: control.DefaultPreferences(), HideOnClose: true, Visible: false, OnClose: func() { closed.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	frame := &vision.Frame{W: 1, H: 1, Pix: []byte{255, 0, 0}}
	p.Publish(frame, nil, true, false, .5, false)
	p.mu.Lock()
	size := len(p.pixels)
	p.mu.Unlock()
	if p.Visible() || size != 0 {
		t.Fatal("hidden preview converted pixels")
	}
	p.Show()
	deadline := time.Now().Add(time.Second)
	for !p.Visible() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !p.Visible() {
		t.Fatal("Show did not reveal preview")
	}
	p.Publish(frame, nil, true, false, .5, false)
	post.Call(p.hwnd, 0x0010, 0, 0)
	deadline = time.Now().Add(time.Second)
	for p.Visible() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p.Visible() || closed.Load() != 0 {
		t.Fatal("preview close exited background app")
	}
	select {
	case <-p.done:
		t.Fatal("hidden preview was destroyed")
	default:
	}
}

func TestPreviewSliderPreservesUntouchedPrecision(t *testing.T) {
	if os.Getenv("MOUSE_PREVIEW_TEST") != "1" {
		t.Skip("set MOUSE_PREVIEW_TEST=1")
	}
	initial := control.DefaultPreferences()
	initial.Scroll = .12345
	applied := make(chan control.Preferences, 1)
	p, err := NewWithPreferences(nil, initial, func(next control.Preferences) error { applied <- next; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	slider := p.sliders[0].track
	user.NewProc("SendMessageW").Call(slider, 0x405, 1, 75)
	post.Call(p.hwnd, 0x0114, 8, slider)
	select {
	case next := <-applied:
		if next.Scroll != .12345 || next.MoveLeft != .75 {
			t.Fatalf("slider changed untouched field: %+v", next)
		}
	case <-time.After(time.Second):
		t.Fatal("slider did not apply")
	}
}
