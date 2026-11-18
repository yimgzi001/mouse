//go:build windows && amd64

package panel

import (
	"image/png"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gesturectl/internal/control"
	"gesturectl/internal/winui"
)

func bindingIndex(t *testing.T, cfg control.Configuration, id string) int {
	t.Helper()
	for i, b := range cfg.Bindings {
		if b.ID == id {
			return i
		}
	}
	t.Fatalf("default binding %s missing", id)
	return -1
}

func homeText(w *Window, row, column int) string {
	buffer := make([]uint16, 256)
	item := listItem{SubItem: int32(column), Text: &buffer[0], TextMax: int32(len(buffer))}
	send(w.controls[idHome], 0x1073, uintptr(row), uintptr(unsafe.Pointer(&item)))
	return syscall.UTF16ToString(buffer)
}

// settle lets the work that a notification put off run.
func settle(w *Window) { w.invoke(func() {}) }

func TestNativeControlsAreThemedAndFollowScaling(t *testing.T) {
	w, _, _ := nativePanel(t)
	var themed bool
	var before, after rect
	w.invoke(func() {
		// Only version 6 of the common controls answers this message.
		var ideal point
		themed = send(w.controls[idSave], 0x1601, 0, uintptr(unsafe.Pointer(&ideal))) != 0 && ideal.X > 0
		user.NewProc("GetWindowRect").Call(w.controls[idSave], uintptr(unsafe.Pointer(&before)))
		w.dpi *= 2
		w.rescale()
		user.NewProc("GetWindowRect").Call(w.controls[idSave], uintptr(unsafe.Pointer(&after)))
	})
	if !themed {
		t.Fatal("buttons are not the themed version 6 controls")
	}
	if width, height := before.Right-before.Left, before.Bottom-before.Top; width <= 0 || after.Right-after.Left != 2*width || after.Bottom-after.Top != 2*height {
		t.Fatalf("doubling the scaling turned the button %v into %v", before, after)
	}
}

func TestNativeHomeListsGesturesInPlainWords(t *testing.T) {
	w, get, _ := nativePanel(t)
	bindings := get().Config.Bindings
	w.invoke(func() {
		if rows := int(send(w.controls[idHome], 0x1004, 0, 0)); rows != len(bindings) {
			t.Errorf("home lists %d rows for %d bindings", rows, len(bindings))
			return
		}
		for i, b := range bindings {
			how, what := control.Describe(b)
			if name, shown, does := homeText(w, i, 0), homeText(w, i, 1), homeText(w, i, 2); name != b.Name || shown != how || does != what || how == "" || what == "" {
				t.Errorf("row %d shows %q, %q, %q for %q, %q, %q", i, name, shown, does, b.Name, how, what)
			}
			if checked := send(w.controls[idHome], 0x102c, uintptr(i), 0xf000) == 0x2000; checked != b.Enabled {
				t.Errorf("row %d checked=%t for enabled=%t", i, checked, b.Enabled)
			}
		}
	})
}

func TestNativeHomeCheckBoxAppliesAtOnce(t *testing.T) {
	w, get, _ := nativePanel(t)
	click, other := bindingIndex(t, get().Config, "right-click"), bindingIndex(t, get().Config, "tab")
	// Looking at another row first must not leave anything behind in the draft.
	w.invoke(func() {
		item := listItem{StateMask: 0x3, State: 0x3}
		send(w.controls[idHome], 0x102b, uintptr(other), uintptr(unsafe.Pointer(&item)))
	})
	settle(w)
	w.invoke(func() { w.checkHome(click, false) })
	settle(w)
	if s := get(); s.Config.Bindings[click].Enabled || s.Revision != 2 {
		t.Fatalf("clearing the box left enabled=%t at revision %d", s.Config.Bindings[click].Enabled, s.Revision)
	}
	w.invoke(func() {
		if w.session.dirty() || w.selected != other {
			t.Errorf("after the change dirty=%t selected=%d", w.session.dirty(), w.selected)
		}
		if send(w.controls[idHome], 0x102c, uintptr(click), 0xf000) != 0x1000 {
			t.Error("the row is still checked")
		}
		w.checkHome(click, true)
	})
	settle(w)
	if s := get(); !s.Config.Bindings[click].Enabled || s.Revision != 3 {
		t.Fatalf("setting the box left enabled=%t at revision %d", s.Config.Bindings[click].Enabled, s.Revision)
	}
}

func TestNativeHomeCheckBoxJoinsOtherUnsavedChanges(t *testing.T) {
	w, get, _ := nativePanel(t)
	click, other := bindingIndex(t, get().Config, "right-click"), bindingIndex(t, get().Config, "tab")
	w.invoke(func() {
		w.selectBinding(other)
		setText(w.controls[idName], "改过的名字")
		w.checkHome(click, false)
	})
	settle(w)
	if s := get(); !s.Config.Bindings[click].Enabled || s.Revision != 1 {
		t.Fatal("a change in the draft was applied behind the user's back")
	}
	w.invoke(func() {
		if b := w.session.config.Bindings; b[click].Enabled || b[other].Name != "改过的名字" {
			t.Errorf("draft holds enabled=%t name=%q", b[click].Enabled, b[other].Name)
		}
		send(w.hwnd, wmCommand, uintptr(idSave), 0)
	})
	if s := get(); s.Config.Bindings[click].Enabled || s.Config.Bindings[other].Name != "改过的名字" || s.Revision != 2 {
		t.Fatalf("saving the draft gave %+v at revision %d", s.Config.Bindings[click], s.Revision)
	}
}

func TestNativeHintSwitchAppliesAtOnceAndSurvivesDefaults(t *testing.T) {
	w, get, _ := nativePanel(t)
	w.invoke(func() {
		if send(w.controls[idHints], bmGetCheck, 0, 0) != 1 {
			t.Error("hints are not on to begin with")
		}
		send(w.controls[idHints], bmSetCheck, 0, 0)
		send(w.hwnd, wmCommand, uintptr(idHints), 0)
	})
	if s := get(); !s.Config.Preferences.HideHints || s.Revision != 2 {
		t.Fatalf("switching hints off left hideHints=%t at revision %d", s.Config.Preferences.HideHints, s.Revision)
	}
	w.invoke(func() {
		send(w.hwnd, wmCommand, uintptr(idResetPreferences), 0)
		send(w.hwnd, wmCommand, uintptr(idSave), 0)
	})
	if !get().Config.Preferences.HideHints {
		t.Fatal("restoring the default sensitivities switched the hints back on")
	}
}

func TestNativeDetailsOpenTheBindingEditor(t *testing.T) {
	w, get, _ := nativePanel(t)
	index := bindingIndex(t, get().Config, "drag")
	w.invoke(func() {
		w.openEditor(index)
		if tab := int(send(w.controls[idTabs], 0x130b, 0, 0)); w.page != pageEditor || tab != pageEditor || w.selected != index {
			t.Errorf("page=%d tab=%d selected=%d", w.page, tab, w.selected)
		}
		if name := textOf(w.controls[idName]); name != get().Config.Bindings[index].Name {
			t.Errorf("editor shows %q", name)
		}
	})
}

// TestNativePanelPictures saves a picture of every page, drawn at the scaling
// of the monitor as the program draws it, for a person to look at.
func TestNativePanelPictures(t *testing.T) {
	dir := os.Getenv("MOUSE_PANEL_SHOTS")
	if dir == "" {
		t.Skip("set MOUSE_PANEL_SHOTS to a directory to save a picture of every page")
	}
	user.NewProc("SetProcessDpiAwarenessContext").Call(^uintptr(3))
	w, _, _ := nativePanel(t)
	w.PublishStatus("摄像头 0 / 1280x720  |  30.0 FPS  |  处理 11.8 ms\r\nright：point  |  最近：右手移动 / start")
	for page, name := range []string{"home", "gain", "editor", "recognition", "files"} {
		w.invoke(func() { w.showPage(page); w.refresh() })
		time.Sleep(400 * time.Millisecond)
		var shot *winui.Canvas
		w.invoke(func() { shot = winui.Capture(w.hwnd) })
		file, err := os.Create(filepath.Join(dir, "panel-"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, shot.Image()); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
