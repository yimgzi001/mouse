//go:build windows

package tray

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	advapi         = syscall.NewLazyDLL("advapi32.dll")
	regCreateKey   = advapi.NewProc("RegCreateKeyExW")
	regSetValue    = advapi.NewProc("RegSetValueExW")
	regDeleteValue = advapi.NewProc("RegDeleteValueW")
)

// Startup is a value under a Run key of the current user: Windows runs its
// command when the user signs in.
type Startup struct {
	// Key is under HKEY_CURRENT_USER.
	Key, Name, Command string
}

// DefaultStartup is the entry Windows itself reads at sign-in.
func DefaultStartup(command string) Startup {
	return Startup{Key: `Software\Microsoft\Windows\CurrentVersion\Run`, Name: "GestureMouse", Command: command}
}

// Command quotes a program path and its arguments into a command line.
func Command(program string, args ...string) string {
	line := `"` + program + `"`
	for _, arg := range args {
		line += " " + syscall.EscapeArg(arg)
	}
	return line
}

// Enabled reports whether the entry starts this command. An entry left by a
// copy of the program somewhere else does not count.
func (s Startup) Enabled() (bool, error) {
	value, err := s.read()
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取开机启动项：%w", err)
	}
	return strings.EqualFold(value, s.Command), nil
}

// Set writes the entry, or removes it.
func (s Startup) Set(on bool) error {
	if err := s.set(on); err != nil {
		return fmt.Errorf("修改开机启动项：%w", err)
	}
	return nil
}

func (s Startup) set(on bool) error {
	key, err := s.open(syscall.KEY_SET_VALUE, on)
	if !on && errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	defer syscall.RegCloseKey(key)
	name, err := syscall.UTF16PtrFromString(s.Name)
	if err != nil {
		return err
	}
	if !on {
		if r, _, _ := regDeleteValue.Call(uintptr(key), uintptr(unsafe.Pointer(name))); r != 0 && syscall.Errno(r) != syscall.ERROR_FILE_NOT_FOUND {
			return syscall.Errno(r)
		}
		return nil
	}
	data, err := syscall.UTF16FromString(s.Command)
	if err != nil {
		return err
	}
	if r, _, _ := regSetValue.Call(uintptr(key), uintptr(unsafe.Pointer(name)), 0, syscall.REG_SZ, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2)); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func (s Startup) open(access uint32, create bool) (syscall.Handle, error) {
	path, err := syscall.UTF16PtrFromString(s.Key)
	if err != nil {
		return 0, err
	}
	var key syscall.Handle
	if !create {
		err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, path, 0, access, &key)
		return key, err
	}
	if r, _, _ := regCreateKey.Call(syscall.HKEY_CURRENT_USER, uintptr(unsafe.Pointer(path)), 0, 0, 0, uintptr(access), 0, uintptr(unsafe.Pointer(&key)), 0); r != 0 {
		return 0, syscall.Errno(r)
	}
	return key, nil
}

func (s Startup) read() (string, error) {
	key, err := s.open(syscall.KEY_QUERY_VALUE, false)
	if err != nil {
		return "", err
	}
	defer syscall.RegCloseKey(key)
	name, err := syscall.UTF16PtrFromString(s.Name)
	if err != nil {
		return "", err
	}
	var kind, size uint32
	if err := syscall.RegQueryValueEx(key, name, nil, &kind, nil, &size); err != nil {
		return "", err
	}
	if kind != syscall.REG_SZ && kind != syscall.REG_EXPAND_SZ {
		return "", nil
	}
	buf := make([]uint16, size/2+1)
	if err := syscall.RegQueryValueEx(key, name, nil, &kind, (*byte)(unsafe.Pointer(&buf[0])), &size); err != nil {
		return "", err
	}
	return syscall.UTF16ToString(buf), nil
}
