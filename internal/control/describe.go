package control

import (
	"fmt"
	"strings"

	"gesturectl/internal/engine"
)

var handWords = map[string]string{"left": "左手", "right": "右手", "any": "任意一只手", "both": "两只手"}

var poseWords = map[string]string{
	"fist": "握拳", "point": "只伸食指", "two": "伸食指和中指", "middle": "只伸中指",
	"eight_back":  "手背朝摄像头比八（拇指和食指成直角）",
	"click_pinch": "拇指和食指捏在一起", "three": "伸食指、中指和无名指", "four": "伸四指，收拇指",
	"five": "五指张开", "middle_ring_pinky": "伸中指、无名指和小指", "thumb_out": "只伸拇指",
	"two_index": "都只伸食指",
}

var triggerWords = map[string]string{
	"exit": "，松开时触发", "repeat": "，保持着会连续触发", "hold": "，保持住",
	"swipe_left": "，向左挥", "swipe_right": "，向右挥", "swipe_up": "，向上挥", "swipe_down": "，向下挥",
}

// Describe says in plain words how the gesture of a binding is made and what
// it does, for people who do not want to read triggers and parameters.
func Describe(b engine.Binding) (how, what string) {
	pose := poseWords[b.Pose]
	if pose == "" {
		pose = b.Pose
		for _, def := range GestureCatalog() {
			if def.ID == b.Pose {
				pose = def.Name
			}
		}
	}
	how = handWords[b.Hand] + pose + triggerWords[b.Trigger]
	if b.Trigger == "long_press" {
		how += fmt.Sprintf("，保持 %g 秒", float64(b.HoldMs)/1000)
	}
	var spec engine.ActionSpec
	for _, candidate := range engine.Catalog() {
		if candidate.Type == b.Action.Type {
			spec = candidate
		}
	}
	param := func(key string) (engine.ParamSpec, string) {
		for _, p := range spec.Params {
			if p.Key == key {
				if v, ok := b.Action.Params[key]; ok {
					return p, fmt.Sprint(v)
				}
				return p, fmt.Sprint(p.Default)
			}
		}
		return engine.ParamSpec{}, fmt.Sprint(b.Action.Params[key])
	}
	value := func(key string) string { _, v := param(key); return v }
	label := func(key string) string {
		p, v := param(key)
		for _, o := range p.Options {
			if o.Value == v {
				return o.Label
			}
		}
		return v
	}
	keys := func() string {
		p, v := param("keys")
		parts := strings.FieldsFunc(v, func(r rune) bool { return r == '+' })
		for i, part := range parts {
			side := ""
			switch {
			case strings.HasPrefix(part, "right_"):
				side, part = "右 ", part[len("right_"):]
			case strings.HasPrefix(part, "left_"):
				side, part = "左 ", part[len("left_"):]
			}
			if part = strings.ReplaceAll(part, "_", " "); part != "" {
				part = strings.ToUpper(part[:1]) + part[1:]
			}
			parts[i] = side + part
		}
		pretty := strings.Join(parts, "+")
		for _, o := range p.Presets {
			if o.Value == v {
				if len(parts) == 1 {
					return o.Label
				}
				return o.Label + "（" + pretty + "）"
			}
		}
		return pretty
	}
	clip := func(s string, n int) string {
		if r := []rune(s); len(r) > n {
			return string(r[:n]) + "…"
		}
		return s
	}
	switch b.Action.Type {
	case "pointer_move":
		what = "光标跟着手移动"
	case "mouse_click":
		what = map[string]string{"1": "点一下", "2": "双击", "3": "三击"}[value("count")] + label("button")
	case "drag":
		what = "按住" + label("button") + "拖动，换手势就松开"
	case "mouse_down":
		what = "按下" + label("button") + "不放"
	case "mouse_up":
		what = "松开" + label("button")
	case "scroll":
		what = "手上下移动，页面跟着滚动"
		if value("axis") == "horizontal" {
			what = "手左右移动，页面跟着横向滚动"
		}
	case "scroll_step":
		what = label("direction") + "滚动 " + value("amount") + " 格"
	case "zoom":
		what = "手上下移动来缩放（Ctrl+滚轮）"
	case "pair_zoom":
		what = "两手拉开放大，靠近缩小"
	case "zoom_step":
		what = label("direction") + "一步"
	case "key_combo":
		what = "按 " + keys()
	case "key_hold":
		what = "按住 " + keys() + "，换手势就松开"
	case "type_text":
		what = "输入文字“" + clip(value("text"), 12) + "”"
	case "window_switcher":
		what = "打开窗口切换（Alt+Tab），手左右移动来选，用确认手势或收手选定"
	case "window_cycle":
		what = "切到下一个窗口"
		if value("direction") == "previous" {
			what = "切回上一个窗口"
		}
	case "media_key":
		what = label("key")
	case "volume":
		what = "手上下移动来调音量"
	case "toggle_pause":
		what = "暂停或恢复手势控制"
	case "stop_hand":
		what = "停止这只手正在做的动作"
	case "confirm_action":
		what = "确认当前动作，比如选定窗口"
	case "cancel_action":
		what = "取消当前动作"
	case "lock_screen":
		what = "锁定屏幕"
	case "open":
		what = "打开 " + clip(value("target"), 24)
	default:
		what = spec.Name
		if what == "" {
			what = b.Action.Type
		}
	}
	return how, what
}
