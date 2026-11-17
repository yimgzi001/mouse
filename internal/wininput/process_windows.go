//go:build windows && amd64

package wininput

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	setPriorityClass      = kernel32.NewProc("SetPriorityClass")
	setProcessInformation = kernel32.NewProc("SetProcessInformation")
)

const (
	aboveNormalPriorityClass = 0x8000
	processPowerThrottling   = 4
	throttleExecutionSpeed   = 0x1
	throttleTimerResolution  = 0x4
)

// RaiseProcessPriority keeps frame processing on time while other programs are
// busy or this one has no focus: it runs the process above normal priority and
// opts it out of the power throttling Windows applies to background processes.
func RaiseProcessPriority() error {
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		return err
	}
	if ok, _, err := setPriorityClass.Call(uintptr(process), aboveNormalPriorityClass); ok == 0 {
		return fmt.Errorf("SetPriorityClass: %w", err)
	}
	if setProcessInformation.Find() != nil {
		return nil
	}
	// Windows 10 rejects the timer-resolution flag, which only Windows 11 knows.
	for _, mask := range []uint32{throttleExecutionSpeed | throttleTimerResolution, throttleExecutionSpeed} {
		state := struct{ Version, ControlMask, StateMask uint32 }{1, mask, 0}
		ok, _, callErr := setProcessInformation.Call(uintptr(process), processPowerThrottling, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
		if ok != 0 {
			return nil
		}
		err = callErr
	}
	return fmt.Errorf("SetProcessInformation: %w", err)
}
