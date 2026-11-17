//go:build windows && amd64

package preview

import (
	"fmt"
	"gesturectl/internal/control"
	"math"
	"syscall"
	"unsafe"
)

type sensitivitySlider struct {
	name         string
	label, track uintptr
}

func preferenceValues(p control.Preferences) []float64 {
	return []float64{p.MoveLeft, p.MoveRight, p.Drag, p.Scroll, p.Switcher, p.Zoom}
}
func withPreferenceValues(p control.Preferences, v []float64) control.Preferences {
	p.MoveLeft, p.MoveRight, p.Drag, p.Scroll, p.Switcher, p.Zoom = v[0], v[1], v[2], v[3], v[4], v[5]
	return p
}

func (p *Window) createSettings(hwnd uintptr) error {
	init := struct{ Size, Classes uint32 }{8, 4}
	ok, _, err := syscall.NewLazyDLL("comctl32.dll").NewProc("InitCommonControlsEx").Call(uintptr(unsafe.Pointer(&init)))
	if ok == 0 {
		return fmt.Errorf("初始化灵敏度滑动条：%w", err)
	}
	instance, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	font, _, _ := gdi.NewProc("GetStockObject").Call(17)
	create := func(class, text string, id int) (uintptr, error) {
		cl, _ := syscall.UTF16PtrFromString(class)
		tx, _ := syscall.UTF16PtrFromString(text)
		style := uintptr(0x50000000)
		if class == "msctls_trackbar32" {
			style |= 0x10
		}
		child, _, err := user.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(cl)), uintptr(unsafe.Pointer(tx)), style, 0, 0, 100, 24, hwnd, uintptr(id), instance, 0)
		if child == 0 {
			return 0, fmt.Errorf("创建灵敏度控件：%w", err)
		}
		user.NewProc("SendMessageW").Call(child, 0x30, font, 1)
		return child, nil
	}
	p.settingsTitle, err = create("STATIC", "独立灵敏度（0.1～5）：松开滑块后生效并保存；可先按 F8 暂停", 900)
	if err != nil {
		return err
	}
	names := []string{"左手移动", "右手移动", "拖拽", "滚动", "切屏", "缩放"}
	values := preferenceValues(p.preferences)
	for n, name := range names {
		label, err := create("STATIC", fmt.Sprintf("%s %.2f", name, values[n]), 910+n*2)
		if err != nil {
			return err
		}
		track, err := create("msctls_trackbar32", "", 911+n*2)
		if err != nil {
			return err
		}
		send := user.NewProc("SendMessageW")
		send.Call(track, 0x406, 1, uintptr(500<<16|10))
		send.Call(track, 0x417, 0, 10)
		send.Call(track, 0x415, 0, 25)
		send.Call(track, 0x405, 1, uintptr(math.Round(values[n]*100)))
		p.sliders = append(p.sliders, sensitivitySlider{name: name, label: label, track: track})
	}
	p.layoutSettings(hwnd)
	return nil
}

func (p *Window) layoutSettings(hwnd uintptr) {
	if p.settingsTitle == 0 {
		return
	}
	var area rect
	user.NewProc("GetClientRect").Call(hwnd, uintptr(unsafe.Pointer(&area)))
	move := user.NewProc("MoveWindow")
	colWidth := max(260, (int(area.Right)-24)/2)
	move.Call(p.settingsTitle, 12, uintptr(max(0, int(area.Bottom)-130)), uintptr(max(1, int(area.Right)-24)), 24, 1)
	for n, s := range p.sliders {
		x := 12 + (n%2)*colWidth
		y := max(0, int(area.Bottom)-104) + (n/2)*30
		move.Call(s.label, uintptr(x), uintptr(y+4), 126, 24, 1)
		move.Call(s.track, uintptr(x+126), uintptr(y), uintptr(max(80, colWidth-138)), 26, 1)
	}
}

func (p *Window) scrollSetting(hwnd uintptr, code uint16, slider uintptr) {
	changed := -1
	for n, s := range p.sliders {
		if s.track == slider {
			changed = n
		}
	}
	if changed < 0 {
		return
	}
	p.mu.Lock()
	next := p.preferences
	callback := p.onPreferences
	patch := p.onPreferencePatch
	p.mu.Unlock()
	base := next
	values := preferenceValues(base)
	for n, s := range p.sliders {
		value, _, _ := user.NewProc("SendMessageW").Call(s.track, 0x400, 0, 0)
		if n == changed {
			values[n] = float64(int32(value)) / 100
		}
		label, _ := syscall.UTF16PtrFromString(fmt.Sprintf("%s %.2f", s.name, values[n]))
		user.NewProc("SetWindowTextW").Call(s.label, uintptr(unsafe.Pointer(label)))
	}
	if code != 8 {
		return
	} // TB_ENDTRACK: apply after mouse/key release
	next = withPreferenceValues(next, values)
	err := next.Validate()
	if err == nil && patch != nil {
		next, err = patch(base, next)
	} else if err == nil && callback != nil {
		err = callback(next)
	}
	p.mu.Lock()
	if err != nil {
		p.lastAction = "设置未保存：" + err.Error()
	} else {
		p.preferences = next
		p.lastAction = "独立灵敏度已应用并保存"
	}
	p.mu.Unlock()
	if err == nil {
		p.displayPreferences(next)
	}
	user.NewProc("InvalidateRect").Call(hwnd, 0, 0)
}

// Called on the preview OS thread only; TBM_SETPOS doesn't emit HSCROLL.
func (p *Window) displayPreferences(next control.Preferences) {
	values := preferenceValues(next)
	for n, s := range p.sliders {
		user.NewProc("SendMessageW").Call(s.track, 0x405, 1, uintptr(math.Round(values[n]*100)))
		label, _ := syscall.UTF16PtrFromString(fmt.Sprintf("%s %.2f", s.name, values[n]))
		user.NewProc("SetWindowTextW").Call(s.label, uintptr(unsafe.Pointer(label)))
	}
}
