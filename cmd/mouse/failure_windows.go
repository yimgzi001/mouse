package main

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	createMutex = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
	messageBox  = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
)

// anotherInstanceRunning reports whether a copy of the program already runs in
// this Windows session. The mutex it creates stays open until the process
// exits; when it cannot be created the program runs unguarded.
func anotherInstanceRunning() bool {
	name, err := syscall.UTF16PtrFromString(`Local\gesturectl-mouse`)
	if err != nil {
		return false
	}
	h, _, err := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return false
	}
	if errors.Is(err, syscall.ERROR_ALREADY_EXISTS) {
		syscall.CloseHandle(syscall.Handle(h))
		return true
	}
	return false
}

func showFailure(text string) {
	body, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	title, _ := syscall.UTF16PtrFromString("手势鼠标")
	const iconError, setForeground = 0x10, 0x10000
	messageBox.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), iconError|setForeground)
}
