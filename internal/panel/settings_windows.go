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
var recognitionNames = []string{"投票窗口", "丢手宽限", "中指确认", "OK 确认", "五指确认"}

func (w *Window) createSettings() {
	w.label("全局基础灵敏度", 1, 20, 4, 480, 30)
	w.label("六项独立基础倍率，范围 0.1～5；动作内倍率默认 1，二者只相乘一次。", 1, 20, 40, 1052, 28)
	for i, name := range sensitivityNames {
		col, row := i%2, i/2
		x := 20 + col*550
		y := 86 + row*48
		w.label(name, 1, x, y+3, 170, 27)
		w.settingEdits = append(w.settingEdits, w.edit(500+i, 1, x+180, y, 300))
	}
	w.add("BUTTON", "交换识别左右手（与 F6 的校正一致）", idSwapHands, 1, 20, 238, 600, 28, 0x10003)
	w.label("识别与确认时间（毫秒）", 1, 20, 290, 700, 30)
	w.label("投票窗口 30～1000；丢手宽限 50～2000；三项手势确认 10～1000。较长时间更稳，响应也更慢。", 1, 20, 326, 1052, 36)
	for i, name := range recognitionNames {
		col, row := i%2, i/2
		x := 20 + col*550
		y := 378 + row*48
		w.label(name+" ms", 1, x, y+3, 170, 27)
		w.settingEdits = append(w.settingEdits, w.edit(510+i, 1, x+180, y, 300))
	}
	w.label("这些更改先进入草稿。点击“保存并应用”后即时生效，并保留当前暂停状态。", 1, 20, 534, 1052, 30)
}
func preferenceValues(p control.Preferences) []float64 {
	return []float64{p.MoveLeft, p.MoveRight, p.Drag, p.Scroll, p.Switcher, p.Zoom}
}
func (w *Window) renderSettings() {
	for i, n := range preferenceValues(w.session.config.Preferences) {
		setText(w.settingEdits[i], strconv.FormatFloat(n, 'f', -1, 64))
	}
	r := w.session.config.Recognition
	for i, n := range []int{r.VoteWindowMs, r.LostGraceMs, r.MiddleConfirmMs, r.OKConfirmMs, r.FiveConfirmMs} {
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
	ints := make([]int, 5)
	for i := range ints {
		n, err := strconv.Atoi(strings.TrimSpace(textOf(w.settingEdits[6+i])))
		lo, hi := 10, 1000
		if i == 0 {
			lo = 30
		}
		if i == 1 {
			lo, hi = 50, 2000
		}
		if err != nil || n < lo || n > hi {
			return fmt.Errorf("%s须为 %d～%d 之间的整数毫秒", recognitionNames[i], lo, hi)
		}
		ints[i] = n
	}
	p := w.session.config.Preferences
	p.MoveLeft, p.MoveRight, p.Drag, p.Scroll, p.Switcher, p.Zoom = values[0], values[1], values[2], values[3], values[4], values[5]
	p.SwapHands = send(w.controls[idSwapHands], bmGetCheck, 0, 0) != 0
	w.session.config.Preferences = p
	w.session.config.Recognition = control.RecognitionSettings{VoteWindowMs: ints[0], LostGraceMs: ints[1], MiddleConfirmMs: ints[2], OKConfirmMs: ints[3], FiveConfirmMs: ints[4]}
	return nil
}
