//go:build windows && amd64

package panel

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"gesturectl/internal/control"
	"gesturectl/internal/engine"
)

func (w *Window) createEditor() {
	w.label("绑定列表 · 启用控制动作，不改变手势识别", pageEditor, 24, 0, 330, 20)
	w.add("LISTBOX", "", idList, pageEditor, 24, 24, 330, 384, 0x310101)
	w.pin(growHeight)
	w.button("新增绑定", idNew, pageEditor, 24, 418, 160)
	w.pin(moveDown)
	w.button("删除选中绑定", idDelete, pageEditor, 194, 418, 160)
	w.pin(moveDown)
	w.add("BUTTON", "启用此绑定", idEnabled, pageEditor, 376, 0, 200, 22, 0x10003)
	w.label("名称", pageEditor, 376, 31, 60, 20)
	w.edit(idName, pageEditor, 440, 28, 576)
	w.pin(growWidth)
	w.label("手别", pageEditor, 376, 61, 60, 20)
	w.combo(idHand, pageEditor, 440, 58, 170)
	w.label("手势", pageEditor, 630, 61, 50, 20)
	w.combo(idPose, pageEditor, 684, 58, 332)
	w.pin(growWidth)
	w.add("STATIC", "", idPoseHelp, pageEditor, 376, 86, 640, 34, 0)
	w.pin(growWidth)
	w.label("触发方式", pageEditor, 376, 127, 70, 20)
	w.combo(idTrigger, pageEditor, 450, 124, 566)
	w.pin(growWidth)
	w.label("确认/等待 ms", pageEditor, 376, 157, 110, 20)
	w.edit(idHold, pageEditor, 490, 154, 90)
	w.label("重复间隔 ms", pageEditor, 630, 157, 110, 20)
	w.edit(idInterval, pageEditor, 744, 154, 90)
	w.label("动作", pageEditor, 376, 187, 60, 20)
	w.combo(idAction, pageEditor, 440, 184, 576)
	w.pin(growWidth)
	w.add("STATIC", "", idActionHelp, pageEditor, 376, 212, 640, 34, 0)
	w.pin(growWidth)
	w.add("STATIC", "", idGainHelp, pageEditor, 376, 342, 640, 34, 0)
	w.pin(growWidth)
	w.label("返回姿势保护（多选；取消所有选择可关闭）", pageEditor, 376, 382, 640, 20)
	w.add("LISTBOX", "", idRelease, pageEditor, 376, 404, 640, 42, 0x210109)
	w.pin(growWidth | growHeight)
}
func fillCombo(h uintptr, labels, values []string, current string) {
	send(h, 0x14b, 0, 0)
	selected := -1
	for i, label := range labels {
		send(h, 0x143, 0, uintptr(unsafe.Pointer(utf16(label))))
		if values[i] == current {
			selected = i
		}
	}
	send(h, 0x14e, arg(selected), 0)
}
func selectedValue(h uintptr, values []string, fallback string) string {
	i := int(int32(send(h, 0x147, 0, 0)))
	if i < 0 || i >= len(values) {
		return fallback
	}
	return values[i]
}
func (w *Window) renderBindings() {
	h := w.controls[idList]
	send(h, 0x184, 0, 0)
	for _, b := range w.session.config.Bindings {
		mark := "○"
		if b.Enabled {
			mark = "✓"
		}
		text := fmt.Sprintf("%s %s · %s · %s → %s", mark, b.Name, handLabel(b.Hand), poseLabel(b.Pose), actionLabel(b.Action.Type))
		send(h, 0x180, 0, uintptr(unsafe.Pointer(utf16(text))))
	}
	send(h, 0x194, arg(w.scale(950)), 0)
	if len(w.session.config.Bindings) > 0 {
		if w.selected < 0 || w.selected >= len(w.session.config.Bindings) {
			w.selected = 0
		}
	} else {
		w.selected = -1
	}
	w.selectBinding(w.selected)
}
func (w *Window) selectBinding(index int) {
	loading := w.loading
	w.loading = true
	defer func() { w.loading = loading }()
	w.selected = index
	send(w.controls[idList], 0x186, arg(index), 0)
	w.selectHome(index)
	valid := index >= 0 && index < len(w.session.config.Bindings)
	for _, id := range []int{idEnabled, idName, idHand, idPose, idTrigger, idHold, idInterval, idAction, idRelease, idDelete} {
		enabled := uintptr(0)
		if valid {
			enabled = 1
		}
		user.NewProc("EnableWindow").Call(w.controls[id], enabled)
	}
	if !valid {
		setText(w.controls[idName], "")
		setText(w.controls[idPoseHelp], "暂无绑定。新增绑定，或在配置管理中恢复默认映射。")
		w.clearParams()
		return
	}
	b := w.session.config.Bindings[index]
	checked := uintptr(0)
	if b.Enabled {
		checked = 1
	}
	send(w.controls[idEnabled], bmSetCheck, checked, 0)
	setText(w.controls[idName], b.Name)
	w.hands = []string{"left", "right", "any", "both"}
	fillCombo(w.controls[idHand], []string{"左手", "右手", "任意单手", "双手"}, w.hands, b.Hand)
	w.fillPoses(b.Hand, b.Pose)
	w.fillActions(b.Hand, b.Pose, b.Action.Type)
	w.fillTriggers(b.Action.Type, b.Trigger)
	setText(w.controls[idHold], strconv.Itoa(b.HoldMs))
	setText(w.controls[idInterval], strconv.Itoa(b.IntervalMs))
	w.renderParams(b.Action)
	w.renderRelease(b.ReleaseTo)
	w.updatePoseHelp()
	w.updateTriggerHelp()
	w.updateGainHelp()
	w.formDirty = false
}
func (w *Window) fillPoses(hand, current string) {
	labels := []string{}
	w.poses = nil
	for _, p := range control.GestureCatalog() {
		if (hand == "both") != (p.ID == "two_index") {
			continue
		}
		w.poses = append(w.poses, p.ID)
		labels = append(labels, p.Name)
	}
	if !contains(w.poses, current) && len(w.poses) > 0 {
		current = w.poses[0]
	}
	fillCombo(w.controls[idPose], labels, w.poses, current)
}
func (w *Window) fillActions(hand, pose, current string) {
	labels := []string{}
	w.actions = nil
	for _, a := range engine.Catalog() {
		if a.NeedsAllowOpen || !control.ActionCompatible(hand, pose, a.Type) {
			continue
		}
		w.actions = append(w.actions, a.Type)
		labels = append(labels, a.Category+" · "+a.Name)
	}
	fillCombo(w.controls[idAction], labels, w.actions, current)
}
func (w *Window) fillTriggers(action, current string) {
	a := actionSpec(action)
	labels := []string{}
	w.triggers = nil
	for _, t := range engine.Triggers() {
		if selectedValue(w.controls[idHand], w.hands, "") == "both" && strings.HasPrefix(t.ID, "swipe_") {
			continue
		}
		if a != nil && a.Continuous != t.Continuous {
			continue
		}
		w.triggers = append(w.triggers, t.ID)
		labels = append(labels, t.Name)
	}
	if !contains(w.triggers, current) && len(w.triggers) > 0 {
		current = w.triggers[0]
	}
	fillCombo(w.controls[idTrigger], labels, w.triggers, current)
}
func (w *Window) clearParams() {
	remove := map[uintptr]bool{}
	for _, p := range w.params {
		remove[p.handle] = true
	}
	for _, h := range w.paramLabels {
		remove[h] = true
	}
	keep := w.layouts[:0]
	for _, l := range w.layouts {
		if !remove[l.handle] {
			keep = append(keep, l)
		}
	}
	w.layouts = keep
	for id, h := range w.controls {
		if remove[h] {
			user.NewProc("DestroyWindow").Call(h)
			delete(w.controls, id)
		}
	}
	w.params = nil
	w.paramLabels = nil
}
func (w *Window) renderParams(action engine.Action) {
	w.clearParams()
	a := actionSpec(action.Type)
	if a == nil {
		return
	}
	if a.NeedsAllowOpen {
		setText(w.controls[idActionHelp], "此动作当前不可用。已导入的停用绑定会保留；请选择其他动作后再启用。")
		return
	}
	help := a.Desc
	for _, p := range a.Params {
		if p.Help != "" {
			if help != "" {
				help += "；"
			}
			help += p.Help
		}
	}
	setText(w.controls[idActionHelp], help)
	for i, s := range a.Params {
		value, ok := action.Params[s.Key]
		if !ok {
			value = s.Default
		}
		label := s.Label
		if s.Type == "number" {
			label += fmt.Sprintf(" (%g～%g)", s.Min, s.Max)
		}
		w.paramLabels = append(w.paramLabels, w.label(label, pageEditor, 376, 253+i*30, 190, 20))
		w.nextID++
		id := w.nextID
		var h uintptr
		var values []string
		switch s.Type {
		case "select":
			h = w.combo(id, pageEditor, 570, 250+i*30, 446)
			labels := []string{}
			for _, o := range s.Options {
				labels = append(labels, o.Label)
				values = append(values, o.Value)
			}
			fillCombo(h, labels, values, fmt.Sprint(value))
		case "bool":
			h = w.add("BUTTON", s.Help, id, pageEditor, 570, 250+i*30, 446, 24, 0x10003)
			if value == true {
				send(h, bmSetCheck, 1, 0)
			}
		default:
			h = w.edit(id, pageEditor, 570, 250+i*30, 446)
			setText(h, fmt.Sprint(value))
		}
		w.pin(growWidth)
		w.params = append(w.params, paramControl{spec: s, handle: h, values: values})
	}
	w.layout()
}
func (w *Window) renderRelease(selected []string) {
	h := w.controls[idRelease]
	enabled := uintptr(1)
	if selectedValue(w.controls[idHand], w.hands, "") == "both" {
		enabled = 0
		selected = nil
	}
	user.NewProc("EnableWindow").Call(h, enabled)
	send(h, 0x184, 0, 0)
	w.releasePoses = nil
	for _, p := range control.GestureCatalog() {
		if p.ID == "two_index" {
			continue
		}
		i := len(w.releasePoses)
		w.releasePoses = append(w.releasePoses, p.ID)
		send(h, 0x180, 0, uintptr(unsafe.Pointer(utf16(p.Name))))
		if contains(selected, p.ID) {
			send(h, 0x185, 1, arg(i))
		}
	}
}
func (w *Window) captureBinding() (engine.Binding, error) {
	b := w.session.config.Bindings[w.selected]
	b.Name = textOf(w.controls[idName])
	b.Enabled = send(w.controls[idEnabled], bmGetCheck, 0, 0) != 0
	b.Hand = selectedValue(w.controls[idHand], w.hands, b.Hand)
	b.Pose = selectedValue(w.controls[idPose], w.poses, b.Pose)
	b.Trigger = selectedValue(w.controls[idTrigger], w.triggers, b.Trigger)
	hold, err := strconv.Atoi(strings.TrimSpace(textOf(w.controls[idHold])))
	if err != nil {
		return b, fmt.Errorf("确认时间须为整数")
	}
	b.HoldMs = hold
	interval, err := strconv.Atoi(strings.TrimSpace(textOf(w.controls[idInterval])))
	if err != nil {
		return b, fmt.Errorf("重复间隔须为整数")
	}
	b.IntervalMs = interval
	if a := actionSpec(b.Action.Type); a == nil || !a.NeedsAllowOpen || len(w.params) > 0 {
		b.Action.Params = map[string]any{}
	}
	for _, p := range w.params {
		var v any
		switch p.spec.Type {
		case "bool":
			v = send(p.handle, bmGetCheck, 0, 0) != 0
		case "select":
			v = selectedValue(p.handle, p.values, fmt.Sprint(p.spec.Default))
		case "number":
			n, e := strconv.ParseFloat(strings.TrimSpace(textOf(p.handle)), 64)
			if e != nil {
				return b, fmt.Errorf("%s 须为数字", p.spec.Label)
			}
			v = n
		default:
			v = textOf(p.handle)
		}
		b.Action.Params[p.spec.Key] = v
	}
	b.ReleaseTo = nil
	for i, p := range w.releasePoses {
		if b.Hand != "both" && int32(send(w.controls[idRelease], 0x187, arg(i), 0)) > 0 {
			b.ReleaseTo = append(b.ReleaseTo, p)
		}
	}
	return b, nil
}
func (w *Window) commitBinding() error {
	if w.selected < 0 || w.selected >= len(w.session.config.Bindings) {
		return nil
	}
	b, err := w.captureBinding()
	if err != nil {
		return err
	}
	b.Action.Type = selectedValue(w.controls[idAction], w.actions, b.Action.Type)
	if !control.ActionCompatible(b.Hand, b.Pose, b.Action.Type) {
		return fmt.Errorf("%s：手别、手势与动作不兼容", b.Name)
	}
	if err = b.Normalize(func(id string) bool {
		for _, p := range control.GestureCatalog() {
			if p.ID == id {
				return true
			}
		}
		return false
	}); err != nil {
		return err
	}
	w.session.config.Bindings[w.selected] = b
	return nil
}
func (w *Window) commitAll() error {
	if err := w.commitBinding(); err != nil {
		return err
	}
	if err := w.commitSettings(); err != nil {
		return err
	}
	w.formDirty = false
	return nil
}
func (w *Window) changeAction() {
	if w.selected < 0 {
		return
	}
	action := selectedValue(w.controls[idAction], w.actions, "")
	b, err := w.captureBinding()
	if err != nil {
		w.notice(err.Error())
		fillCombo(w.controls[idAction], actionLabels(w.actions), w.actions, w.session.config.Bindings[w.selected].Action.Type)
		return
	}
	b.Action = engine.Action{Type: action, Params: map[string]any{}}
	w.session.config.Bindings[w.selected] = b
	w.loading = true
	w.fillTriggers(action, b.Trigger)
	w.renderParams(b.Action)
	w.updateTriggerHelp()
	w.loading = false
	w.formDirty = true
	w.updateGainHelp()
	w.notice("动作已修改，请检查参数并保存。")
}
func (w *Window) changeInput(id int) {
	if w.selected < 0 {
		return
	}
	b, err := w.captureBinding()
	if err != nil {
		w.notice(err.Error())
		return
	}
	w.loading = true
	if id == idHand {
		w.fillPoses(b.Hand, b.Pose)
		b.Pose = selectedValue(w.controls[idPose], w.poses, b.Pose)
	}
	oldAction := b.Action.Type
	w.fillActions(b.Hand, b.Pose, oldAction)
	if !contains(w.actions, oldAction) && len(w.actions) > 0 {
		send(w.controls[idAction], 0x14e, 0, 0)
	}
	b.Action.Type = selectedValue(w.controls[idAction], w.actions, oldAction)
	if b.Action.Type != oldAction {
		b.Action.Params = map[string]any{}
	}
	if b.Hand == "both" {
		b.ReleaseTo = nil
	}
	w.renderRelease(b.ReleaseTo)
	w.fillTriggers(b.Action.Type, b.Trigger)
	w.session.config.Bindings[w.selected] = b
	w.renderParams(b.Action)
	w.updatePoseHelp()
	w.updateTriggerHelp()
	w.loading = false
	w.formDirty = true
	w.updateGainHelp()
	w.notice("手别/手势已修改，动作和触发方式已按兼容性筛选；请检查后保存。")
}
func (w *Window) updatePoseHelp() {
	pose := selectedValue(w.controls[idPose], w.poses, "")
	for _, p := range control.GestureCatalog() {
		if p.ID != pose {
			continue
		}
		description := p.Desc
		if pose != "two_index" {
			f := []string{}
			for i, name := range []string{"拇指", "食指", "中指", "无名指", "小指"} {
				state := map[string]string{"open": "伸直", "closed": "弯曲", "any": "不限", "": "不限"}[p.Fingers[i]]
				f = append(f, name+state)
			}
			description += "\r\n" + strings.Join(f, " · ")
			if p.Touch != "" {
				description += "；拇指与 " + map[string]string{"index": "食指", "middle": "中指", "ring": "无名指", "pinky": "小指"}[p.Touch] + "接触"
			}
		}
		setText(w.controls[idPoseHelp], description)
		return
	}
}
func (w *Window) updateTriggerHelp() {
	trigger := selectedValue(w.controls[idTrigger], w.triggers, "")
	for _, t := range engine.Triggers() {
		if t.ID != trigger {
			continue
		}
		enabled := uintptr(0)
		if t.HoldLabel != "" {
			enabled = 1
		}
		user.NewProc("EnableWindow").Call(w.controls[idHold], enabled)
		enabled = 0
		if t.UsesRepeat {
			enabled = 1
		}
		user.NewProc("EnableWindow").Call(w.controls[idInterval], enabled)
		if !t.UsesRepeat {
			setText(w.controls[idInterval], "0")
		}
		if t.HoldLabel == "" {
			setText(w.controls[idHold], "0")
		}
		if t.UsesRepeat && textOf(w.controls[idInterval]) == "0" {
			setText(w.controls[idInterval], "400")
		}
		if t.DefaultHold > 0 && textOf(w.controls[idHold]) == "0" {
			setText(w.controls[idHold], strconv.Itoa(t.DefaultHold))
		}
		return
	}
}
func (w *Window) updateGainHelp() {
	if w.selected < 0 {
		return
	}
	a := selectedValue(w.controls[idAction], w.actions, "")
	p := w.session.config.Preferences
	base := 0.0
	label := ""
	switch a {
	case "pointer_move":
		hand := selectedValue(w.controls[idHand], w.hands, "")
		if hand == "left" {
			base = p.MoveLeft
			label = "左手移动"
		} else if hand == "right" {
			base = p.MoveRight
			label = "右手移动"
		} else {
			setText(w.controls[idGainHelp], fmt.Sprintf("基础倍率按实际手别选择：左 %.2f / 右 %.2f，再乘本动作倍率。", p.MoveLeft, p.MoveRight))
			return
		}
	case "drag":
		base = p.Drag
		label = "拖拽"
	case "scroll":
		base = p.Scroll
		label = "滚动"
	case "window_switcher", "window_cycle":
		base = p.Switcher
		label = "切屏"
	case "pair_zoom", "zoom":
		base = p.Zoom
		label = "缩放"
	}
	if base == 0 {
		setText(w.controls[idGainHelp], "离散动作不使用灵敏度。任意单手与指定手重叠时，指定手绑定优先。")
		return
	}
	gain := 1.0
	for _, p := range w.params {
		if p.spec.Key == "sensitivity" {
			if n, err := strconv.ParseFloat(textOf(p.handle), 64); err == nil {
				gain = n
			}
		}
	}
	setText(w.controls[idGainHelp], fmt.Sprintf("%s基础 %.2f × 动作倍率 %.2f = %.2f。基础值在“灵敏度”页设置。", label, base, gain, base*gain))
}
func (w *Window) newBinding() {
	ids := map[string]bool{}
	for _, b := range w.session.config.Bindings {
		ids[b.ID] = true
	}
	n := 1
	for ids[fmt.Sprintf("custom_%d", n)] {
		n++
	}
	b := engine.Binding{ID: fmt.Sprintf("custom_%d", n), Name: "新绑定", Enabled: false, Hand: "left", Pose: "fist", Trigger: "enter", Action: engine.Action{Type: "key_combo", Params: map[string]any{"keys": "enter"}}}
	w.session.config.Bindings = append(w.session.config.Bindings, b)
	w.selected = len(w.session.config.Bindings) - 1
	w.renderAll()
	w.notice("已新增停用绑定，请设置手势和动作；启用前先解决重复手势冲突。")
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func actionSpec(id string) *engine.ActionSpec {
	for _, a := range engine.Catalog() {
		if a.Type == id {
			return &a
		}
	}
	return nil
}
func actionLabel(id string) string {
	if a := actionSpec(id); a != nil {
		return a.Name
	}
	return id
}
func actionLabels(ids []string) []string {
	labels := []string{}
	for _, id := range ids {
		a := actionSpec(id)
		if a != nil {
			labels = append(labels, a.Category+" · "+a.Name)
		} else {
			labels = append(labels, id)
		}
	}
	return labels
}
func poseLabel(id string) string {
	for _, p := range control.GestureCatalog() {
		if p.ID == id {
			return p.Name
		}
	}
	return id
}
func handLabel(id string) string {
	return map[string]string{"left": "左", "right": "右", "any": "任意", "both": "双手"}[id]
}
