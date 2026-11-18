//go:build windows

package overlay

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"gesturectl/internal/control"
	"gesturectl/internal/gesture"
	"gesturectl/internal/winui"
)

var (
	isWindow            = user.NewProc("IsWindow")
	isWindowVisible     = user.NewProc("IsWindowVisible")
	getWindowLongPtr    = user.NewProc("GetWindowLongPtrW")
	getWindowRect       = user.NewProc("GetWindowRect")
	getForegroundWindow = user.NewProc("GetForegroundWindow")
)

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
	}
}

func TestNativeHintFollowsTheCursorWithoutTakingTheFocus(t *testing.T) {
	if os.Getenv("MOUSE_OVERLAY_TEST") != "1" {
		t.Skip("set MOUSE_OVERLAY_TEST=1 to open the hint window; no input is sent")
	}
	o, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	visible := func() bool { v, _, _ := isWindowVisible.Call(o.hwnd); return v != 0 }
	hint := []control.Hint{{ID: 1, Side: gesture.SideRight, Fingers: [gesture.NumFingers]bool{false, true}, Text: "右手移动", Bound: true, Active: true, Progress: 1}}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(30 * time.Millisecond):
				o.Frame(hint, nil)
			}
		}
	}()
	waitFor(t, "the hint shows", visible)
	if style, _, _ := getWindowLongPtr.Call(o.hwnd, ^uintptr(19)); style&exStyle != exStyle {
		t.Errorf("extended style %#x lacks %#x", style, exStyle&^style)
	}
	if foreground, _, _ := getForegroundWindow.Call(); foreground == o.hwnd {
		t.Error("the hint took the focus")
	}
	var cursor point
	var r rect
	getCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	getWindowRect.Call(o.hwnd, uintptr(unsafe.Pointer(&r)))
	if dx, dy := r.Left-cursor.X, r.Top-cursor.Y; dx < -400 || dx > 400 || dy < -400 || dy > 400 || r.Right <= r.Left {
		t.Errorf("the hint at %+v is not by the cursor at %+v", r, cursor)
	}
	o.SetEnabled(false)
	waitFor(t, "the hint hides when it is turned off", func() bool { return !visible() })
	o.Paused(true)
	waitFor(t, "the pause notice shows with the hints off", visible)
	waitFor(t, "the pause notice ends", func() bool { return !visible() })
	o.Close()
	if alive, _, _ := isWindow.Call(o.hwnd); alive != 0 {
		t.Fatal("the window outlives Close")
	}
}

func TestNativeHintPictures(t *testing.T) {
	dir := os.Getenv("MOUSE_OVERLAY_SHOTS")
	if dir == "" {
		t.Skip("set MOUSE_OVERLAY_SHOTS to a directory to save pictures of the hint at 200%")
	}
	font := winui.Font(10, 192, 600, winui.Grey, "Microsoft YaHei UI")
	defer deleteObject.Call(font)
	text := func(s string) ([]byte, int, int) { return winui.TextMask(font, s) }
	left := winui.Hand{Fingers: [5]int{winui.Curled, winui.Raised, winui.Raised}, Left: true}
	right := winui.Hand{Fingers: [5]int{winui.Curled, winui.Raised}}
	pinch := winui.Hand{Fingers: [5]int{winui.Raised, winui.Raised}, Pinch: true}
	for name, rows := range map[string][]Row{
		"confirming": {{Look: Confirming, Text: "两指右键", Hand: left, HasHand: true, Progress: .45}},
		"running":    {{Look: Running, Text: "右手移动", Hand: right, HasHand: true, Progress: 1}},
		"fired":      {{Look: Fired, Text: "捏合左键", Hand: pinch, HasHand: true, Progress: 1, Glow: 1}},
		"two-hands":  {{Look: Confirming, Text: "两指右键", Hand: left, HasHand: true, Progress: .7}, {Look: Running, Text: "右手移动", Hand: right, HasHand: true, Progress: 1}},
		"seen":       {{Look: Seen, Text: "握拳", HasHand: true}},
		"paused":     {{Look: Pause, Text: "已暂停，按 F8 恢复"}},
		"resumed":    {{Look: Resume, Text: "已恢复手势控制"}},
	} {
		c := Draw(rows, 192, text)
		file, err := os.Create(filepath.Join(dir, "overlay-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, c.Image())
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}
