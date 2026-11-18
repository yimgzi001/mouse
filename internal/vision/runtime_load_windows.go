package vision

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var loadLibraryEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LoadLibraryExW")

// preloadRuntime loads onnxruntime.dll so that the libraries it needs are
// looked for in its own folder first. Windows otherwise looks next to the
// program and in System32 only, ignoring a Visual C++ runtime shipped in the
// runtime folder or preferring an older one in System32. onnxruntime_go later
// loads the same path and gets this module back.
func preloadRuntime(path string) error {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	const loadWithAlteredSearchPath = 0x8
	h, _, err := loadLibraryEx.Call(uintptr(unsafe.Pointer(name)), 0, loadWithAlteredSearchPath)
	if h != 0 {
		return nil
	}
	if errors.Is(err, syscall.ERROR_MOD_NOT_FOUND) {
		return fmt.Errorf("a library it needs is missing, usually the Microsoft Visual C++ 2015-2022 x64 runtime: %w", err)
	}
	return err
}
