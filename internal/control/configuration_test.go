package control

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"gesturectl/internal/engine"
	"gesturectl/internal/vision"
)

func TestLegacyConfigurationMigratesWithoutLosingPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := []byte(`{"moveLeft":0.5,"moveRight":0.44,"drag":0.5,"scroll":0.75,"switcher":3.19,"zoom":1,"swapHands":true}`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != ConfigurationVersion || cfg.Preferences.MoveRight != .44 || cfg.Preferences.Switcher != 3.19 || !cfg.Preferences.SwapHands {
		t.Fatalf("migration lost values: %+v", cfg)
	}
	if err := SaveConfiguration(path, cfg); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".v1.bak")
	if err != nil || string(backup) != string(legacy) {
		t.Fatalf("backup lost: %s %v", backup, err)
	}
	for _, b := range cfg.Bindings {
		if b.Action.Type == "pointer_move" || b.Action.Type == "drag" {
			if b.Action.Params["sensitivity"] != float64(1) {
				t.Fatalf("migration double multiplies gain: %+v", b)
			}
		}
	}
	loaded, err := LoadConfiguration(path)
	if err != nil || loaded.Preferences != cfg.Preferences {
		t.Fatalf("round trip: %+v %v", loaded, err)
	}
}

// version2File is the default configuration as version 2 stored it, with
// the given recognition times and an extra OK return pose on the Tab binding.
func version2File(t *testing.T, recognition map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(DefaultConfiguration(DefaultPreferences()))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err = json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["version"] = 2
	object["recognition"] = recognition
	gestures := object["gestures"].([]any)
	for n, g := range gestures {
		if g == "middle_ring_pinky" {
			gestures[n] = "ok"
		}
	}
	changed := 0
	for _, item := range object["bindings"].([]any) {
		b := item.(map[string]any)
		switch b["id"] {
		case "right-alt":
			b["pose"], b["name"] = "ok", "右手 OK 右 Alt"
			changed++
		case "tab":
			b["releaseTo"] = []any{"fist", "ok"}
			changed++
		}
	}
	if changed != 2 || slices.Contains(gestures, any("middle_ring_pinky")) {
		t.Fatalf("default configuration no longer has the bindings this test rewrites: %s", data)
	}
	if data, err = json.Marshal(object); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestVersion2ConfigurationReplacesOKWithMiddleRingPinky(t *testing.T) {
	custom := DefaultRecognition()
	custom.VoteWindowMs, custom.LostGraceMs, custom.MiddleConfirmMs, custom.MiddleRingPinkyConfirmMs, custom.FiveConfirmMs = 200, 300, 70, 90, 50
	for _, tc := range []struct {
		name        string
		recognition map[string]any
		want        RecognitionSettings
	}{
		{"old defaults", map[string]any{"voteWindowMs": 150, "lostGraceMs": 200, "middleConfirmMs": 60, "okConfirmMs": 80, "fiveConfirmMs": 30}, DefaultRecognition()},
		{"customised", map[string]any{"voteWindowMs": 200, "lostGraceMs": 300, "middleConfirmMs": 70, "okConfirmMs": 90, "fiveConfirmMs": 50}, custom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			stored := version2File(t, tc.recognition)
			if err := os.WriteFile(path, stored, 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfiguration(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Version != ConfigurationVersion || cfg.Recognition != tc.want {
				t.Fatalf("version %d recognition %+v, want %+v", cfg.Version, cfg.Recognition, tc.want)
			}
			if slices.Contains(cfg.Gestures, "ok") || !slices.Contains(cfg.Gestures, "middle_ring_pinky") {
				t.Fatalf("gestures not upgraded: %v", cfg.Gestures)
			}
			for _, b := range cfg.Bindings {
				if b.ID == "right-alt" && (b.Pose != "middle_ring_pinky" || b.Name != "右手后三指 右 Alt") {
					t.Fatalf("right Alt binding not upgraded: %+v", b)
				}
				if b.ID == "tab" && !slices.Equal(b.ReleaseTo, []string{"fist", "middle_ring_pinky"}) {
					t.Fatalf("return poses not upgraded: %v", b.ReleaseTo)
				}
			}
			if err := SaveConfiguration(path, cfg); err != nil {
				t.Fatal(err)
			}
			if backup, err := os.ReadFile(path + ".v2.bak"); err != nil || string(backup) != string(stored) {
				t.Fatalf("version 2 backup lost: %s %v", backup, err)
			}
			if loaded, err := LoadConfiguration(path); err != nil || loaded.Recognition != tc.want {
				t.Fatalf("round trip: %+v %v", loaded.Recognition, err)
			}
		})
	}
}

func TestConfigurationEmptyBindingsDeepCopyAndValidation(t *testing.T) {
	cfg := DefaultConfiguration(DefaultPreferences())
	copy := cfg.Clone()
	copy.Bindings[0].Action.Params["sensitivity"] = float64(3)
	if cfg.Bindings[0].Action.Params["sensitivity"] != float64(1) {
		t.Fatal("configuration aliases action params")
	}
	for _, b := range []func(*Configuration){
		func(c *Configuration) { c.Version = 999 },
		func(c *Configuration) { c.Preferences.MoveLeft = math.NaN() },
		func(c *Configuration) { c.Bindings[0].Action.Params["sensitivity"] = math.Inf(1) },
		func(c *Configuration) {
			for n := range c.Bindings {
				if c.Bindings[n].Action.Type == "key_combo" {
					c.Bindings[n].Action.Params = map[string]any{"key": "right_alt"}
					break
				}
			}
		},
		func(c *Configuration) { c.Bindings = append(c.Bindings, c.Bindings[0]) },
		func(c *Configuration) { c.Bindings[0].Pose = "not-a-gesture" },
		func(c *Configuration) {
			c.Bindings[0].ID = "different"
			c.Bindings = append(c.Bindings, DefaultBindings()[0])
		},
	} {
		bad := cfg.Clone()
		b(&bad)
		if err := bad.Normalize(); err == nil {
			t.Fatalf("accepted invalid configuration: %+v", bad)
		}
	}
	cfg.Bindings = []engine.Binding{}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "empty.json")
	if err := SaveConfiguration(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfiguration(path)
	if err != nil || loaded.Bindings == nil || len(loaded.Bindings) != 0 {
		t.Fatalf("empty bindings restored defaults: %+v %v", loaded, err)
	}
}

func TestServiceRemapsDisablesAndPreservesRecognition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	inj := &recordingInput{}
	svc, err := NewService(path, DefaultConfiguration(DefaultPreferences()), inj)
	if err != nil {
		t.Fatal(err)
	}
	snap := svc.Snapshot()
	for n := range snap.Config.Bindings {
		if snap.Config.Bindings[n].ID == "tab" {
			snap.Config.Bindings[n].Action.Params["keys"] = "esc"
		}
	}
	updated, err := svc.Apply(snap.Revision, snap.Config)
	if err != nil {
		t.Fatal(err)
	}
	now := feed(svc.Controller(), testHand("ccocc", 0, 0), time.Unix(100, 0), 20)
	if countKey(inj, 0x1B, true) != 1 || countKey(inj, 0x09, true) != 0 {
		t.Fatalf("remap not live: %v", inj.keys)
	}
	if _, err := svc.Apply(snap.Revision, snap.Config); err == nil {
		t.Fatal("stale revision replaced mapping")
	}
	for n := range updated.Config.Bindings {
		if updated.Config.Bindings[n].Pose == "middle" {
			updated.Config.Bindings[n].Enabled = false
		}
	}
	if _, err := svc.Apply(updated.Revision, updated.Config); err != nil {
		t.Fatal(err)
	}
	before := len(inj.keys)
	now = feed(svc.Controller(), testHand("ccocc", 0, 0), now, 20)
	states := svc.Controller().Process([]vision.Hand{testHand("ccocc", 0, 0)}, now)
	if len(inj.keys) != before || len(inj.buttons) != 0 || len(states) != 1 || states[0].RawPose != "middle" || states[0].Pose != "middle" {
		t.Fatalf("disable changed recognition or sent input: %+v %+v", states, inj)
	}
	loaded, err := LoadConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range loaded.Bindings {
		if b.Pose == "middle" && b.Enabled {
			t.Fatal("disabled mapping was not persisted")
		}
	}
}

func TestServiceDiskFailureAndPreferencesDoNotLoseBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	svc, err := NewService(path, DefaultConfiguration(DefaultPreferences()), &recordingInput{})
	if err != nil {
		t.Fatal(err)
	}
	snap := svc.Snapshot()
	for n := range snap.Config.Bindings {
		if snap.Config.Bindings[n].ID == "tab" {
			snap.Config.Bindings[n].Action.Params["keys"] = "esc"
		}
	}
	snap, err = svc.Apply(snap.Revision, snap.Config)
	if err != nil {
		t.Fatal(err)
	}
	p := snap.Config.Preferences
	p.MoveRight = .8
	snap, err = svc.UpdatePreferences(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range snap.Config.Bindings {
		if b.ID == "tab" && b.Action.Params["keys"] != "esc" {
			t.Fatal("preference update reset mapping")
		}
	}
	failure := errors.New("disk failure")
	svc.save = func(string, Configuration) error { return failure }
	next := snap.Config.Clone()
	next.Preferences.MoveRight = 1.2
	if _, err := svc.Apply(snap.Revision, next); !errors.Is(err, failure) {
		t.Fatalf("missing failure: %v", err)
	}
	if svc.Snapshot().Revision != snap.Revision || svc.Snapshot().Config.Preferences.MoveRight != .8 {
		t.Fatal("failed disk write changed running snapshot")
	}
}

func TestPreferenceHelpersAndPreviewPatchPreserveNewConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	cfg := DefaultConfiguration(DefaultPreferences())
	for n := range cfg.Bindings {
		if cfg.Bindings[n].ID == "tab" {
			cfg.Bindings[n].Action.Params["keys"] = "esc"
		}
	}
	if err := SaveConfiguration(path, cfg); err != nil {
		t.Fatal(err)
	}
	p := cfg.Preferences
	p.MoveRight = .8
	if err := SavePreferences(path, p); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range loaded.Bindings {
		if b.ID == "tab" && b.Action.Params["keys"] != "esc" {
			t.Fatal("preference helper erased binding")
		}
	}
	svc, err := NewService(path, loaded, &recordingInput{})
	if err != nil {
		t.Fatal(err)
	}
	base := svc.Snapshot().Config.Preferences
	updated := base
	updated.Scroll = 2
	if _, err := svc.UpdatePreferences(updated); err != nil {
		t.Fatal(err)
	}
	staleSlider := base
	staleSlider.MoveLeft = .75
	snap, err := svc.PatchPreferences(base, staleSlider)
	if err != nil || snap.Config.Preferences.Scroll != 2 || snap.Config.Preferences.MoveLeft != .75 {
		t.Fatalf("stale slider overwrote panel: %+v %v", snap, err)
	}
}

func TestServiceApplyReleasesDragAndKeepsPause(t *testing.T) {
	inj := &recordingInput{}
	svc, err := NewService(filepath.Join(t.TempDir(), "settings.json"), DefaultConfiguration(DefaultPreferences()), inj)
	if err != nil {
		t.Fatal(err)
	}
	now := feed(svc.Controller(), rightHand("coocc"), time.Unix(100, 0), 20)
	if len(inj.buttons) != 1 {
		t.Fatal("drag did not start")
	}
	snap := svc.Snapshot()
	if _, err := svc.Apply(snap.Revision, snap.Config); err != nil {
		t.Fatal(err)
	}
	if len(inj.buttons) != 2 || inj.buttons[1] != "left up" {
		t.Fatalf("apply did not release drag: %v", inj.buttons)
	}
	svc.Controller().SetPaused(true)
	snap = svc.Snapshot()
	snap.Config.Preferences.MoveLeft = .7
	if _, err := svc.Apply(snap.Revision, snap.Config); err != nil {
		t.Fatal(err)
	}
	feed(svc.Controller(), rightHand("coocc"), now, 20)
	if !svc.Controller().Paused() || len(inj.buttons) != 2 {
		t.Fatal("apply resumed paused input")
	}
}

func TestServiceApplyDoesNotReplayHeldInput(t *testing.T) {
	for _, tc := range []struct {
		name, spec string
		left       float32
		pinch      int
	}{
		{"click", "ccccc", .99, 0}, {"Tab", "ccocc", .99, 0}, {"RightAlt", "ccooo", .01, 1},
		{"drag", "coocc", .01, 0}, {"selector", "ooooo", .01, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inj := &recordingInput{}
			svc, err := NewService(filepath.Join(t.TempDir(), "settings.json"), DefaultConfiguration(DefaultPreferences()), inj)
			if err != nil {
				t.Fatal(err)
			}
			h := testHand(tc.spec, 0, tc.pinch)
			h.LeftScore = tc.left
			now := feed(svc.Controller(), h, time.Unix(100, 0), 20)
			snap := svc.Snapshot()
			if _, err := svc.Apply(snap.Revision, snap.Config); err != nil {
				t.Fatal(err)
			}
			keys, buttons := len(inj.keys), len(inj.buttons)
			feed(svc.Controller(), h, now, 20)
			if len(inj.keys) != keys || len(inj.buttons) != buttons {
				t.Fatalf("save replayed held %s: keys=%v buttons=%v", tc.name, inj.keys, inj.buttons)
			}
			svc.Controller().Abort()
			assertReleased(t, inj)
		})
	}
}

func TestServiceApplyKeepsPendingKeyboardReleaseProtection(t *testing.T) {
	inj := &recordingInput{}
	svc, err := NewService(filepath.Join(t.TempDir(), "settings.json"), DefaultConfiguration(DefaultPreferences()), inj)
	if err != nil {
		t.Fatal(err)
	}
	now := feed(svc.Controller(), testHand("ccocc", 0, 0), time.Unix(100, 0), 20)
	snap := svc.Snapshot()
	if _, err := svc.Apply(snap.Revision, snap.Config); err != nil {
		t.Fatal(err)
	}
	now = feed(svc.Controller(), testHand("ccccc", 0, 0), now, 20)
	if len(inj.buttons) != 0 {
		t.Fatalf("save lost pending Tab release guard: %v", inj.buttons)
	}
	now = feed(svc.Controller(), testHand("coooo", 0, 0), now, 12)
	feed(svc.Controller(), testHand("ccccc", 0, 0), now, 20)
	if len(inj.buttons) != 2 {
		t.Fatal("normal click did not rearm after stable release")
	}
}

func TestHandCalibrationDoesNotReplayHeldGestureAsOtherSideKey(t *testing.T) {
	inj := &recordingInput{}
	svc, err := NewService(filepath.Join(t.TempDir(), "settings.json"), DefaultConfiguration(DefaultPreferences()), inj)
	if err != nil {
		t.Fatal(err)
	}
	h := testHand("ccocc", 0, 0)
	now := feed(svc.Controller(), h, time.Unix(100, 0), 20)
	if countKey(inj, 0x09, true) != 1 {
		t.Fatal("initial left middle did not Tab")
	}
	if _, err := svc.ToggleHands(); err != nil {
		t.Fatal(err)
	}
	now = feed(svc.Controller(), h, now, 20)
	if countKey(inj, 0x0D, true) != 0 {
		t.Fatal("hand correction replayed held middle as Enter")
	}
	now = feed(svc.Controller(), testHand("coooo", 0, 0), now, 12)
	feed(svc.Controller(), h, now, 20)
	if countKey(inj, 0x0D, true) != 1 {
		t.Fatal("corrected hand did not Enter after fresh gesture")
	}
}

func TestStoredBindingsKeepTheirOwnParametersInAnyOrder(t *testing.T) {
	want := DefaultConfiguration(DefaultPreferences())
	slices.Reverse(want.Bindings)
	if err := want.Normalize(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := SaveConfiguration(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Bindings, want.Bindings) {
		t.Fatalf("bindings read back as\n%+v\nwant\n%+v", got.Bindings, want.Bindings)
	}
}
