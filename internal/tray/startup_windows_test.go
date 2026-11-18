//go:build windows

package tray

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func TestCommandQuotesTheProgramAndItsArguments(t *testing.T) {
	got := Command(`C:\Program Files\手势\mouse.exe`, "-background", `-config=C:\my files\settings.json`, "-camera=1")
	want := `"C:\Program Files\手势\mouse.exe" -background "-config=C:\my files\settings.json" -camera=1`
	if got != want {
		t.Fatalf("command line %s, want %s", got, want)
	}
}

// The entry goes under a scratch key of the current user, removed afterwards.
func TestStartupWritesFindsAndRemovesItsEntry(t *testing.T) {
	key := fmt.Sprintf(`Software\gesturectl-test-%d`, os.Getpid())
	t.Cleanup(func() {
		path, _ := syscall.UTF16PtrFromString(key)
		advapi.NewProc("RegDeleteKeyW").Call(syscall.HKEY_CURRENT_USER, uintptr(unsafe.Pointer(path)))
	})
	entry := Startup{Key: key, Name: "GestureMouse", Command: Command(`C:\手势\mouse.exe`, "-background")}
	check := func(want bool) {
		t.Helper()
		if on, err := entry.Enabled(); err != nil || on != want {
			t.Fatalf("enabled %t %v, want %t", on, err, want)
		}
	}
	check(false)
	if err := entry.Set(false); err != nil {
		t.Fatalf("removing an entry that was never there: %v", err)
	}
	if err := entry.Set(true); err != nil {
		t.Fatal(err)
	}
	check(true)
	elsewhere := entry
	elsewhere.Command = Command(`D:\旧版\mouse.exe`, "-background")
	if on, err := elsewhere.Enabled(); err != nil || on {
		t.Fatalf("an entry for another copy counted: %t %v", on, err)
	}
	if err := entry.Set(false); err != nil {
		t.Fatal(err)
	}
	check(false)
}
