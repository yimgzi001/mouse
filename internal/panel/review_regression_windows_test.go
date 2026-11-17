//go:build windows && amd64

package panel

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestNativeUnrelatedBindingEditPreservesPreferencePrecision(t *testing.T) {
	w, get, _ := nativePanel(t)
	w.invoke(func() {
		w.session.config.Preferences.Scroll = .12345
		w.renderAll()
		setText(w.controls[idName], "只修改名称")
		send(w.hwnd, wmCommand, uintptr(idSave), 0)
	})
	if got := get().Config.Preferences.Scroll; got != .12345 {
		t.Fatalf("unrelated save rounded sensitivity to %v", got)
	}
}

func TestNativePanelVisibleFromHiddenConsoleLaunch(t *testing.T) {
	if os.Getenv("MOUSE_PANEL_TEST") != "1" {
		t.Skip("set MOUSE_PANEL_TEST=1")
	}
	if os.Getenv("MOUSE_PANEL_HIDDEN_HELPER") == "1" {
		w, _, _ := nativePanel(t)
		w.invoke(func() {
			visible, _, _ := user.NewProc("IsWindowVisible").Call(w.hwnd)
			if visible == 0 {
				t.Error("native panel inherited hidden console startup state")
			}
		})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestNativePanelVisibleFromHiddenConsoleLaunch$", "-test.v")
	cmd.Env = append(os.Environ(), "MOUSE_PANEL_HIDDEN_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hidden startup child: %v\n%s", err, out)
	}
}
