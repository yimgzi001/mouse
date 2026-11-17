package engine

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Option is one choice of a select parameter.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ParamSpec describes one parameter of an action so that the configuration
// page can show a matching input and the engine can check the value.
type ParamSpec struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Type is "select", "number", "text", "keys" or "bool".
	Type    string   `json:"type"`
	Options []Option `json:"options,omitempty"`
	Default any      `json:"default"`
	Min     float64  `json:"min,omitempty"`
	Max     float64  `json:"max,omitempty"`
	Help    string   `json:"help,omitempty"`
	// Presets are ready-made values offered for a "keys" parameter.
	Presets []Option `json:"presets,omitempty"`
}

// ActionSpec describes one kind of action.
type ActionSpec struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Desc     string `json:"desc,omitempty"`
	// Continuous actions stay in effect while the gesture is held and can only
	// be bound with the "hold" trigger; the others fire once.
	Continuous bool `json:"continuous"`
	// Exclusive actions suspend every other binding while they run.
	Exclusive bool `json:"exclusive,omitempty"`
	// Sticky actions keep running when the pose changes; they end when the
	// hand is withdrawn or a stop action fires.
	Sticky bool `json:"sticky,omitempty"`
	// NeedsAllowOpen marks actions that only work when the configuration file
	// permits starting programs.
	NeedsAllowOpen bool        `json:"needsAllowOpen,omitempty"`
	Params         []ParamSpec `json:"params,omitempty"`

	resource string
}

// TriggerSpec describes when a binding fires.
type TriggerSpec struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	// Continuous is true for the trigger used by continuous actions.
	Continuous bool `json:"continuous"`
	// HoldLabel names what holdMs means for this trigger, "" if unused.
	HoldLabel   string `json:"holdLabel,omitempty"`
	DefaultHold int    `json:"defaultHold,omitempty"`
	UsesRepeat  bool   `json:"usesRepeat,omitempty"`
}

// Triggers lists the available triggers.
func Triggers() []TriggerSpec {
	return []TriggerSpec{
		{ID: "enter", Name: "做出手势时（一次）", Desc: "手势稳定后触发一次，松开后才能再次触发", HoldLabel: "额外确认时间"},
		{ID: "exit", Name: "松开手势时（一次）", Desc: "手势结束或手离开画面时触发一次"},
		{ID: "long_press", Name: "长时间保持后（一次）", Desc: "手势保持到指定时间后触发一次", HoldLabel: "保持时间", DefaultHold: 800},
		{ID: "repeat", Name: "保持期间重复触发", Desc: "手势保持期间按间隔反复触发", HoldLabel: "首次触发前等待", UsesRepeat: true},
		{ID: "hold", Name: "保持期间持续生效", Desc: "用于移动光标、滚动、拖拽等跟随手部移动的动作", Continuous: true, HoldLabel: "开始前等待"},
		{ID: "swipe_left", Name: "保持手势向左挥", Desc: "保持该手势快速向左移动手掌"},
		{ID: "swipe_right", Name: "保持手势向右挥", Desc: "保持该手势快速向右移动手掌"},
		{ID: "swipe_up", Name: "保持手势向上挥", Desc: "保持该手势快速向上移动手掌"},
		{ID: "swipe_down", Name: "保持手势向下挥", Desc: "保持该手势快速向下移动手掌"},
	}
}

var (
	buttonOptions = []Option{{"left", "左键"}, {"right", "右键"}, {"middle", "中键"}, {"x1", "侧键（后退）"}, {"x2", "侧键（前进）"}}
	sourceOptions = []Option{{"index_tip", "食指指尖"}, {"palm", "手掌中心"}}
	buttonParam   = ParamSpec{Key: "button", Label: "鼠标键", Type: "select", Options: buttonOptions, Default: "left"}
	sourceParam   = ParamSpec{Key: "source", Label: "跟随部位", Type: "select", Options: sourceOptions, Default: "index_tip", Help: "指尖更灵活，手掌中心更稳"}
	gainParam     = ParamSpec{Key: "sensitivity", Label: "灵敏度", Type: "number", Default: 1.0, Min: 0.1, Max: 5}
	naturalParam  = ParamSpec{Key: "natural", Label: "反向（内容跟手走）", Type: "bool", Default: false}
)

var keyPresets = []Option{
	{"ctrl+c", "复制"}, {"ctrl+v", "粘贴"}, {"ctrl+x", "剪切"}, {"ctrl+z", "撤销"}, {"ctrl+y", "重做"},
	{"ctrl+a", "全选"}, {"ctrl+s", "保存"}, {"ctrl+f", "查找"},
	{"enter", "回车"}, {"space", "空格"}, {"esc", "Esc"}, {"delete", "删除"}, {"backspace", "退格"},
	{"up", "方向键 上"}, {"down", "方向键 下"}, {"left", "方向键 左"}, {"right", "方向键 右"},
	{"pageup", "上一页"}, {"pagedown", "下一页"}, {"home", "Home"}, {"end", "End"},
	{"alt+left", "后退"}, {"alt+right", "前进"}, {"f5", "刷新"}, {"f11", "全屏"},
	{"ctrl+t", "新建标签页"}, {"ctrl+w", "关闭标签页"}, {"ctrl+shift+t", "恢复关闭的标签页"},
	{"ctrl+tab", "下一个标签页"}, {"ctrl+shift+tab", "上一个标签页"},
	{"ctrl+equals", "放大"}, {"ctrl+minus", "缩小"}, {"ctrl+0", "恢复缩放"},
	{"win+d", "显示桌面"}, {"win+tab", "任务视图"}, {"alt+f4", "关闭窗口"},
	{"win+up", "最大化窗口"}, {"win+down", "最小化窗口"}, {"win+left", "窗口贴左"}, {"win+right", "窗口贴右"},
	{"win+ctrl+left", "上一个虚拟桌面"}, {"win+ctrl+right", "下一个虚拟桌面"},
	{"win+shift+s", "截图"}, {"win+e", "资源管理器"}, {"win+l", "锁屏（建议用“锁定屏幕”动作）"},
	{"media_play_pause", "播放/暂停"}, {"media_next", "下一首"}, {"media_prev", "上一首"},
	{"volume_up", "音量+"}, {"volume_down", "音量-"}, {"volume_mute", "静音"},
}

var catalog = []ActionSpec{
	{Type: "pointer_move", Name: "移动光标", Category: "鼠标", Continuous: true, resource: "pointer",
		Desc: "光标跟随手的相对移动", Params: []ParamSpec{sourceParam, gainParam}},
	{Type: "mouse_click", Name: "鼠标点击", Category: "鼠标", Params: []ParamSpec{buttonParam,
		{Key: "count", Label: "次数", Type: "select", Options: []Option{{"1", "单击"}, {"2", "双击"}, {"3", "三击"}}, Default: "1"}}},
	{Type: "drag", Name: "按住拖拽", Category: "鼠标", Continuous: true, resource: "pointer",
		Desc: "手势开始时按下鼠标键，保持期间移动光标，结束时松开", Params: []ParamSpec{buttonParam, sourceParam, gainParam}},
	{Type: "mouse_down", Name: "按下鼠标键（不松开）", Category: "鼠标", Desc: "与“松开鼠标键”配对使用", Params: []ParamSpec{buttonParam}},
	{Type: "mouse_up", Name: "松开鼠标键", Category: "鼠标", Params: []ParamSpec{buttonParam}},
	{Type: "scroll", Name: "滚动（跟随手掌）", Category: "鼠标", Continuous: true, resource: "wheel",
		Desc: "手掌移动多少就滚动多少，停手即停", Params: []ParamSpec{
			{Key: "axis", Label: "方向", Type: "select", Options: []Option{{"vertical", "上下"}, {"horizontal", "左右"}}, Default: "vertical"}, naturalParam, gainParam}},
	{Type: "scroll_step", Name: "滚动一步", Category: "鼠标", Params: []ParamSpec{
		{Key: "direction", Label: "方向", Type: "select", Options: []Option{{"up", "向上"}, {"down", "向下"}, {"left", "向左"}, {"right", "向右"}}, Default: "down"},
		{Key: "amount", Label: "格数", Type: "number", Default: 3.0, Min: 1, Max: 30}}},
	{Type: "zoom", Name: "缩放（跟随手掌上下）", Category: "鼠标", Continuous: true, resource: "wheel",
		Desc: "相当于按住 Ctrl 滚动滚轮：手向上放大，向下缩小", Params: []ParamSpec{naturalParam, gainParam}},
	{Type: "pair_zoom", Name: "双手距离缩放", Category: "鼠标", Continuous: true, Exclusive: true, resource: "wheel", Params: []ParamSpec{gainParam}},
	{Type: "zoom_step", Name: "缩放一步", Category: "鼠标", Params: []ParamSpec{
		{Key: "direction", Label: "方向", Type: "select", Options: []Option{{"in", "放大"}, {"out", "缩小"}}, Default: "in"},
		{Key: "amount", Label: "格数", Type: "number", Default: 1.0, Min: 1, Max: 10}}},

	{Type: "key_combo", Name: "按键 / 组合键", Category: "键盘", Params: []ParamSpec{
		{Key: "keys", Label: "按键", Type: "keys", Default: "ctrl+c", Presets: keyPresets,
			Help: "用 + 连接同时按下的键，如 ctrl+shift+esc；用空格分隔先后按的组合，如 ctrl+k ctrl+c"}}},
	{Type: "key_hold", Name: "按住按键", Category: "键盘", Continuous: true,
		Desc: "手势保持期间按住，结束时松开", Params: []ParamSpec{
			{Key: "keys", Label: "按键", Type: "keys", Default: "space", Help: "只能写一组，如 space 或 ctrl+shift"}}},
	{Type: "type_text", Name: "输入一段文字", Category: "键盘", Params: []ParamSpec{
		{Key: "text", Label: "文字", Type: "text", Default: ""}}},

	{Type: "window_switcher", Name: "切换窗口（Alt+Tab）", Category: "窗口", Continuous: true, Exclusive: true, Sticky: true, resource: "switcher",
		Desc: "打开窗口切换器，手掌左右移动选择，确认动作或撤手确认", Params: []ParamSpec{gainParam}},
	{Type: "window_cycle", Name: "快切窗口", Category: "窗口", Exclusive: true, Params: []ParamSpec{
		{Key: "direction", Label: "方向", Type: "select", Default: "next", Options: []Option{{"next", "Alt+Tab"}, {"previous", "Alt+Shift+Tab"}}}}},

	{Type: "media_key", Name: "媒体键", Category: "媒体与音量", Params: []ParamSpec{
		{Key: "key", Label: "按键", Type: "select", Default: "media_play_pause", Options: []Option{
			{"media_play_pause", "播放/暂停"}, {"media_next", "下一首"}, {"media_prev", "上一首"}, {"media_stop", "停止"},
			{"volume_up", "音量+"}, {"volume_down", "音量-"}, {"volume_mute", "静音"}}}}},
	{Type: "volume", Name: "调节音量（跟随手掌上下）", Category: "媒体与音量", Continuous: true, resource: "volume",
		Desc: "手向上增大音量，向下减小"},

	{Type: "toggle_pause", Name: "暂停 / 恢复手势控制", Category: "系统", Desc: "暂停期间只有绑定了本动作的手势还会生效"},
	{Type: "stop_hand", Name: "停止这只手的动作", Category: "系统", Desc: "取消该手当前动作"},
	{Type: "confirm_action", Name: "确认当前动作", Category: "系统", Desc: "确认窗口选择等当前动作"},
	{Type: "cancel_action", Name: "取消当前动作", Category: "系统", Desc: "取消窗口选择等当前动作"},
	{Type: "lock_screen", Name: "锁定屏幕", Category: "系统"},
	{Type: "open", Name: "打开程序 / 文件 / 网址", Category: "系统", NeedsAllowOpen: true,
		Desc: "出于安全考虑默认关闭：需要手动把配置文件里的 allowOpen 改为 true 才会生效", Params: []ParamSpec{
			{Key: "target", Label: "路径或网址", Type: "text", Default: ""}}},
}

// Catalog returns the available actions.
func Catalog() []ActionSpec { return catalog }

// SupportsInput reports whether an action can consume a single hand or a
// composite. Composite inputs support distance zoom and discrete actions;
// single-hand follow controllers require actual single-hand coordinates.
func SupportsInput(action string, both bool) bool {
	spec := findSpec(action)
	if spec == nil {
		return false
	}
	if both {
		return action == "pair_zoom" || !spec.Continuous && !isFinishAction(action)
	}
	return action != "pair_zoom"
}

func findSpec(actionType string) *ActionSpec {
	for i := range catalog {
		if catalog[i].Type == actionType {
			return &catalog[i]
		}
	}
	return nil
}

func findTrigger(id string) *TriggerSpec {
	for _, t := range Triggers() {
		if t.ID == id {
			return &t
		}
	}
	return nil
}

// Action is the action part of a binding.
type Action struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params,omitempty"`
}

// Binding connects a gesture to an action: when Hand shows Pose in the way
// Trigger says, Action happens.
type Binding struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Hand is "left", "right", "any" or "both".
	Hand    string `json:"hand"`
	Pose    string `json:"pose"`
	Trigger string `json:"trigger"`
	// HoldMs is how long the pose must have been steady before the binding
	// fires or starts.
	HoldMs int `json:"holdMs"`
	// IntervalMs is the time between firings of a "repeat" trigger.
	IntervalMs int    `json:"intervalMs,omitempty"`
	Action     Action `json:"action"`
	// ReleaseTo consumes the next declared return pose until it is released.
	ReleaseTo []string `json:"releaseTo,omitempty"`
}

var bindingIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

const (
	maxHoldMs       = 10000
	minIntervalMs   = 50
	defaultInterval = 400
	maxTextLength   = 500
)

// Normalize checks the binding, fills in defaults and drops unknown
// parameters. hasPose reports whether a pose id exists.
func (b *Binding) Normalize(hasPose func(string) bool) error {
	label := b.Name
	if label == "" {
		label = b.ID
	}
	if !bindingIDPattern.MatchString(b.ID) {
		return fmt.Errorf("binding id %q: use 1 to 40 letters, digits, - or _", b.ID)
	}
	if len([]rune(b.Name)) > 60 {
		return fmt.Errorf("binding %s: name is longer than 60 characters", b.ID)
	}
	switch b.Hand {
	case "left", "right", "any", "both":
	case "":
		b.Hand = "any"
	default:
		return fmt.Errorf("binding %s: hand %q is not left, right or any", label, b.Hand)
	}
	if b.Hand == "both" && b.Pose != "two_index" || b.Hand != "both" && b.Pose == "two_index" {
		return fmt.Errorf("binding %s: gesture %q requires hand both", label, b.Pose)
	}
	if b.Pose != "two_index" && !hasPose(b.Pose) {
		return fmt.Errorf("binding %s: unknown gesture %q", label, b.Pose)
	}
	trigger := findTrigger(b.Trigger)
	if trigger == nil {
		return fmt.Errorf("binding %s: unknown trigger %q", label, b.Trigger)
	}
	spec := findSpec(b.Action.Type)
	if spec == nil {
		return fmt.Errorf("binding %s: unknown action %q", label, b.Action.Type)
	}
	if !SupportsInput(b.Action.Type, b.Hand == "both") {
		return fmt.Errorf("binding %s: action %s does not support hand %s", label, b.Action.Type, b.Hand)
	}
	if b.Hand == "both" && strings.HasPrefix(b.Trigger, "swipe_") {
		return fmt.Errorf("binding %s: composite gesture does not provide swipe events", label)
	}
	for _, pose := range b.ReleaseTo {
		if !hasPose(pose) {
			return fmt.Errorf("binding %s: unknown release gesture %q", label, pose)
		}
	}
	b.ReleaseTo = append([]string(nil), b.ReleaseTo...)
	if spec.Continuous != trigger.Continuous {
		if spec.Continuous {
			return fmt.Errorf("binding %s: action %s follows the hand, it needs the trigger \"hold\"", label, spec.Type)
		}
		return fmt.Errorf("binding %s: action %s fires once, it cannot use the trigger \"hold\"", label, spec.Type)
	}
	if b.HoldMs < 0 || b.HoldMs > maxHoldMs {
		return fmt.Errorf("binding %s: holdMs must be between 0 and %d", label, maxHoldMs)
	}
	if trigger.HoldLabel == "" {
		b.HoldMs = 0
	} else if b.HoldMs == 0 {
		b.HoldMs = trigger.DefaultHold
	}
	if trigger.UsesRepeat {
		if b.IntervalMs == 0 {
			b.IntervalMs = defaultInterval
		}
		if b.IntervalMs < minIntervalMs || b.IntervalMs > maxHoldMs {
			return fmt.Errorf("binding %s: intervalMs must be between %d and %d", label, minIntervalMs, maxHoldMs)
		}
	} else {
		b.IntervalMs = 0
	}

	params := map[string]any{}
	for _, p := range spec.Params {
		v, given := b.Action.Params[p.Key]
		if !given || v == nil {
			v = p.Default
		}
		switch p.Type {
		case "select":
			s, ok := v.(string)
			if !ok || !hasOption(p.Options, s) {
				return fmt.Errorf("binding %s: %s has no option %v", label, p.Key, v)
			}
		case "number":
			n, ok := v.(float64)
			if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < p.Min || n > p.Max {
				return fmt.Errorf("binding %s: %s must be a number between %v and %v", label, p.Key, p.Min, p.Max)
			}
		case "bool":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("binding %s: %s must be true or false", label, p.Key)
			}
		case "text":
			s, ok := v.(string)
			if !ok || len([]rune(s)) > maxTextLength {
				return fmt.Errorf("binding %s: %s must be text of at most %d characters", label, p.Key, maxTextLength)
			}
		case "keys":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("binding %s: %s must be key names", label, p.Key)
			}
			combos, err := ParseKeys(s)
			if err != nil {
				return fmt.Errorf("binding %s: %v", label, err)
			}
			if spec.Continuous && len(combos) != 1 {
				return fmt.Errorf("binding %s: only one key combination can be held", label)
			}
			v = strings.Join(strings.Fields(strings.ToLower(s)), " ")
		}
		params[p.Key] = v
	}
	b.Action.Params = params
	return nil
}

func hasOption(options []Option, value string) bool {
	for _, o := range options {
		if o.Value == value {
			return true
		}
	}
	return false
}
