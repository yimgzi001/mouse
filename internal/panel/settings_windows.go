//go:build windows && amd64

package panel

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"gesturectl/internal/control"
)

const idSwapHands = 450

var sensitivityNames = []string{"左手移动", "右手移动", "拖拽", "滚动", "切屏", "缩放"}

// recognitionFields follows the order of recognitionValues; the ranges match
// control.RecognitionSettings validation.
var recognitionFields = []struct {
	name, unit, word string
	lo, hi           int
}{
	{"投票窗口", "ms", "毫秒", 30, 1000},
	{"丢手宽限", "ms", "毫秒", 50, 2000},
	{"中指确认", "ms", "毫秒", 10, 1000},
	{"后三指确认", "ms", "毫秒", 10, 1000},
	{"五指确认", "ms", "毫秒", 10, 1000},
	{"最小掌宽", "px", "像素", 0, 200},
	{"新手观察", "ms", "毫秒", 0, 1000},
}

func recognitionValues(r control.RecognitionSettings) []int {
	return []int{r.VoteWindowMs, r.LostGraceMs, r.MiddleConfirmMs, r.MiddleRingPinkyConfirmMs, r.FiveConfirmMs, r.MinPalmPx, r.NewHandMs}
}

func (w *Window) createSettings() {
	w.label("六项基础倍率，范围 0.1～5：数字越大，手移动同样的距离，动得越多。动作内倍率默认 1，二者只相乘一次。", pageGain, 24, 0, 992, 20)
	for i, name := range sensitivityNames {
		col, row := i%2, i/2
		x, y := 24+col*500, 36+row*36
		w.label(name, pageGain, x, y+3, 150, 20)
		w.settingEdits = append(w.settingEdits, w.edit(500+i, pageGain, x+160, y, 200))
	}
	w.add("BUTTON", "交换识别左右手（与 F6 的校正一致）", idSwapHands, pageGain, 24, 152, 600, 24, 0x10003)
	w.label("这一页的修改先进入草稿。点击“保存并应用”后即时生效，并保留当前暂停状态。", pageGain, 24, 192, 992, 20)
	w.label("确认时间越长越稳，响应也越慢。最小掌宽：画面里手掌小于它的远处手不接管（0 为不限）；新手观察：刚出现的手在这段时间里只能移动指针，不点击、不按键、不切窗口。", pageRecognition, 24, 0, 992, 38)
	for i, f := range recognitionFields {
		col, row := i%2, i/2
		x, y := 24+col*500, 52+row*36
		w.label(f.name+" "+f.unit, pageRecognition, x, y+3, 150, 20)
		w.settingEdits = append(w.settingEdits, w.edit(510+i, pageRecognition, x+160, y, 200))
	}
	w.label("这一页的修改先进入草稿。点击“保存并应用”后即时生效，并保留当前暂停状态。", pageRecognition, 24, 204, 992, 20)
}
func preferenceValues(p control.Preferences) []float64 {
	return []float64{p.MoveLeft, p.MoveRight, p.Drag, p.Scroll, p.Switcher, p.Zoom}
}
func (w *Window) renderSettings() {
	for i, n := range preferenceValues(w.session.config.Preferences) {
		setText(w.settingEdits[i], strconv.FormatFloat(n, 'f', -1, 64))
	}
	for i, n := range recognitionValues(w.session.config.Recognition) {
		setText(w.settingEdits[6+i], strconv.Itoa(n))
	}
	checked := uintptr(0)
	if w.session.config.Preferences.SwapHands {
		checked = 1
	}
	send(w.controls[idSwapHands], bmSetCheck, checked, 0)
}
func (w *Window) commitSettings() error {
	values := make([]float64, 6)
	for i := range values {
		n, err := strconv.ParseFloat(strings.TrimSpace(textOf(w.settingEdits[i])), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < .1 || n > 5 {
			return fmt.Errorf("%s须为 0.1～5 之间的数字", sensitivityNames[i])
		}
		values[i] = n
	}
	ints := make([]int, len(recognitionFields))
	for i, f := range recognitionFields {
		n, err := strconv.Atoi(strings.TrimSpace(textOf(w.settingEdits[6+i])))
		if err != nil || n < f.lo || n > f.hi {
			return fmt.Errorf("%s须为 %d～%d 之间的整数%s", f.name, f.lo, f.hi, f.word)
		}
		ints[i] = n
	}
	p := w.session.config.Preferences
	p.MoveLeft, p.MoveRight, p.Drag, p.Scroll, p.Switcher, p.Zoom = values[0], values[1], values[2], values[3], values[4], values[5]
	p.SwapHands = send(w.controls[idSwapHands], bmGetCheck, 0, 0) != 0
	w.session.config.Preferences = p
	w.session.config.Recognition = control.RecognitionSettings{VoteWindowMs: ints[0], LostGraceMs: ints[1], MiddleConfirmMs: ints[2], MiddleRingPinkyConfirmMs: ints[3], FiveConfirmMs: ints[4], MinPalmPx: ints[5], NewHandMs: ints[6]}
	return nil
}
