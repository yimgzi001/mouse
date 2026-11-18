package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gesturectl/internal/engine"
)

func TestDescribeSaysDefaultBindingsInPlainWords(t *testing.T) {
	want := map[string][2]string{
		"move-right":  {"右手只伸食指，保持住", "光标跟着手移动"},
		"left-click":  {"右手拇指和食指捏在一起", "点一下左键"},
		"right-click": {"左手伸食指和中指", "点一下右键"},
		"scroll":      {"右手伸食指和中指，保持住", "手上下移动，页面跟着滚动"},
		"drag":        {"右手伸食指、中指和无名指，保持住", "按住左键拖动，换手势就松开"},
		"tab":         {"左手只伸小指", "按 Tab"},
		"enter":       {"右手只伸小指", "按 回车"},
		"backspace":   {"右手只伸中指，横着指向自己的左边", "按 退格"},
		"pair-zoom":   {"两只手都只伸食指，保持住", "两手拉开放大，靠近缩小"},
	}
	seen := 0
	for _, b := range DefaultConfiguration(DefaultPreferences()).Bindings {
		how, what := Describe(b)
		if how == "" || what == "" {
			t.Errorf("%s is described as %q, %q", b.ID, how, what)
		}
		if w, ok := want[b.ID]; ok {
			seen++
			if how != w[0] || what != w[1] {
				t.Errorf("%s is described as %q, %q; want %q, %q", b.ID, how, what, w[0], w[1])
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("%d of %d expected default bindings exist", seen, len(want))
	}
}

func TestDescribeSaysWhichSideAKeyIsOn(t *testing.T) {
	b := engine.Binding{Hand: "right", Pose: "middle_ring_pinky", Trigger: "enter", Action: engine.Action{Type: "key_combo", Params: map[string]any{"keys": "ctrl+right_alt"}}}
	if _, what := Describe(b); !strings.Contains(what, "Ctrl+右 Alt") {
		t.Fatalf("ctrl+right_alt is described as %q", what)
	}
}

func TestDescribeFollowsTriggersAndParameters(t *testing.T) {
	for _, c := range []struct {
		b         engine.Binding
		how, what string
	}{
		{engine.Binding{Hand: "any", Pose: "five", Trigger: "long_press", HoldMs: 800, Action: engine.Action{Type: "key_combo", Params: map[string]any{"keys": "ctrl+c"}}}, "任意一只手五指张开，保持 0.8 秒", "按 复制（Ctrl+C）"},
		{engine.Binding{Hand: "left", Pose: "four", Trigger: "swipe_left", Action: engine.Action{Type: "media_key", Params: map[string]any{"key": "media_next"}}}, "左手伸四指，收拇指，向左挥", "下一首"},
		{engine.Binding{Hand: "right", Pose: "fist", Trigger: "enter", Action: engine.Action{Type: "mouse_click", Params: map[string]any{"button": "left", "count": "2"}}}, "右手握拳", "双击左键"},
		{engine.Binding{Hand: "right", Pose: "three", Trigger: "hold", Action: engine.Action{Type: "scroll", Params: map[string]any{"axis": "horizontal"}}}, "右手伸食指、中指和无名指，保持住", "手左右移动，页面跟着横向滚动"},
		{engine.Binding{Hand: "right", Pose: "mystery", Trigger: "exit", Action: engine.Action{Type: "unknown"}}, "右手mystery，松开时触发", "unknown"},
	} {
		if how, what := Describe(c.b); how != c.how || what != c.what {
			t.Errorf("described as %q, %q; want %q, %q", how, what, c.how, c.what)
		}
	}
}

func TestHideHintsIsStoredOnlyWhenSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	cfg := DefaultConfiguration(DefaultPreferences())
	if err := SaveConfiguration(path, cfg); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || strings.Contains(string(data), "hideHints") {
		t.Fatalf("a configuration with hints on stores the switch: %v", err)
	}
	cfg.Preferences.HideHints = true
	if err := SaveConfiguration(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfiguration(path)
	if err != nil || !loaded.Preferences.HideHints {
		t.Fatalf("hideHints did not survive saving: %v", err)
	}
}
