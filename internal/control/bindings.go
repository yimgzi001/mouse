package control

import "gesturectl/internal/engine"

// DefaultBindings returns a fresh set so configuration never mutates a shared
// binding or parameter map. Specific thumb/OK poses beat generic finger counts.
func DefaultBindings() []engine.Binding {
	return bindingsFor(DefaultPreferences())
}

func bindingsFor(p Preferences) []engine.Binding {
	return []engine.Binding{
		{ID: "move-left", Name: "左手移动", Enabled: true, Hand: "left", Pose: "point", Trigger: "hold", Action: engine.Action{Type: "pointer_move", Params: map[string]any{"sensitivity": float64(1)}}},
		{ID: "move-right", Name: "右手移动", Enabled: true, Hand: "right", Pose: "point", Trigger: "hold", Action: engine.Action{Type: "pointer_move", Params: map[string]any{"sensitivity": float64(1)}}},
		{ID: "left-click", Name: "捏合左键", Enabled: true, Hand: "right", Pose: "click_pinch", Trigger: "enter", HoldMs: 80, Action: engine.Action{Type: "mouse_click", Params: map[string]any{"button": "left"}}},
		{ID: "fist-left-click", Name: "左拳单击左键", Enabled: true, Hand: "left", Pose: "fist", Trigger: "enter", HoldMs: 100, Action: engine.Action{Type: "mouse_click", Params: map[string]any{"button": "left"}}},
		{ID: "right-click", Name: "两指右键", Enabled: true, Hand: "left", Pose: "two", Trigger: "enter", HoldMs: 100, Action: engine.Action{Type: "mouse_click", Params: map[string]any{"button": "right"}}},
		{ID: "scroll", Name: "页面滚动", Enabled: true, Hand: "any", Pose: "three", Trigger: "hold", HoldMs: 100, Action: engine.Action{Type: "scroll"}},
		{ID: "switcher", Name: "窗口切换", Enabled: true, Hand: "right", Pose: "five", Trigger: "hold", Action: engine.Action{Type: "window_switcher"}},
		{ID: "quick-next", Name: "掌向右快切", Enabled: true, Hand: "right", Pose: "four", Trigger: "swipe_right", Action: engine.Action{Type: "window_cycle", Params: map[string]any{"direction": "next"}}},
		{ID: "quick-prev", Name: "掌向左快切", Enabled: true, Hand: "right", Pose: "four", Trigger: "swipe_left", Action: engine.Action{Type: "window_cycle", Params: map[string]any{"direction": "previous"}}},
		{ID: "drag", Name: "右手两指拖拽", Enabled: true, Hand: "right", Pose: "two", Trigger: "hold", HoldMs: 120, Action: engine.Action{Type: "drag", Params: map[string]any{"sensitivity": float64(1)}}},
		{ID: "tab", Name: "左中指 Tab", Enabled: true, Hand: "left", Pose: "middle", Trigger: "enter", ReleaseTo: []string{"fist"}, Action: engine.Action{Type: "key_combo", Params: map[string]any{"keys": "tab"}}},
		{ID: "enter", Name: "右中指回车", Enabled: true, Hand: "right", Pose: "middle", Trigger: "enter", Action: engine.Action{Type: "key_combo", Params: map[string]any{"keys": "enter"}}},
		{ID: "right-alt", Name: "右手后三指 右 Alt", Enabled: true, Hand: "right", Pose: "middle_ring_pinky", Trigger: "enter", Action: engine.Action{Type: "key_combo", Params: map[string]any{"keys": "right_alt"}}},
		{ID: "stop", Name: "右拳确认/停止", Enabled: true, Hand: "right", Pose: "fist", Trigger: "enter", Action: engine.Action{Type: "confirm_action"}},
		{ID: "pair-zoom", Name: "双食指缩放", Enabled: true, Hand: "both", Pose: "two_index", Trigger: "hold", HoldMs: 100, Action: engine.Action{Type: "pair_zoom", Params: map[string]any{"sensitivity": float64(1)}}},
	}
}

// HelpLines is shared by startup logging and the live preview.
func HelpLines() []string {
	return []string{
		"一指：移动；左手握拳：左键；左手两指：右键",
		"三指（食指+中指+无名指）上下移动：滚动页面",
		"右手五指唤出后掌心左右选窗，右手握拳确认",
		"右手四指并拢、拇指收起：右挥快切，左挥反向快切",
		"右手两指：拖拽，收起松开；双食指拉开/合拢：缩放",
		"只伸中指：左手 Tab、右手回车；右手中指+无名指+小指：右 Alt",
	}
}
