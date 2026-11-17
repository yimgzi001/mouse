package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
)

// Version 3 replaced the OK gesture with middle_ring_pinky; version 2 files
// are upgraded when read.
const ConfigurationVersion = 3
const maxConfigurationSize = 1 << 20

var canonicalGestures = []string{"fist", "point", "two", "middle", "click_pinch", "three", "four", "five", "middle_ring_pinky", "thumb_out"}

type RecognitionSettings struct {
	VoteWindowMs             int `json:"voteWindowMs"`
	LostGraceMs              int `json:"lostGraceMs"`
	MiddleConfirmMs          int `json:"middleConfirmMs"`
	MiddleRingPinkyConfirmMs int `json:"middleRingPinkyConfirmMs"`
	FiveConfirmMs            int `json:"fiveConfirmMs"`
	// MinPalmPx is the smallest palm, wrist to middle knuckle in camera pixels,
	// a newly found hand may have; 0 accepts hands of any size.
	MinPalmPx int `json:"minPalmPx"`
	// NewHandMs is how long a newly found hand may only move the pointer.
	NewHandMs int `json:"newHandMs"`
}

func DefaultRecognition() RecognitionSettings {
	return RecognitionSettings{
		VoteWindowMs:             150,
		LostGraceMs:              400,
		MiddleConfirmMs:          60,
		MiddleRingPinkyConfirmMs: 80,
		FiveConfirmMs:            150,
		MinPalmPx:                45,
		NewHandMs:                200,
	}
}

func (r RecognitionSettings) validate() error {
	for _, field := range []struct {
		name, unit      string
		value, min, max int
	}{
		{"投票窗口", "ms", r.VoteWindowMs, 30, 1000},
		{"丢手宽限", "ms", r.LostGraceMs, 50, 2000},
		{"中指确认", "ms", r.MiddleConfirmMs, 10, 1000},
		{"后三指确认", "ms", r.MiddleRingPinkyConfirmMs, 10, 1000},
		{"五指确认", "ms", r.FiveConfirmMs, 10, 1000},
		{"最小掌宽", "px", r.MinPalmPx, 0, 200},
		{"新手观察", "ms", r.NewHandMs, 0, 1000},
	} {
		if field.value < field.min || field.value > field.max {
			return fmt.Errorf("%s须在 %d～%d %s 之间", field.name, field.min, field.max, field.unit)
		}
	}
	return nil
}

func (r RecognitionSettings) settings(p Preferences) gesture.Config {
	cfg := p.recognizerSettings()
	cfg.VoteWindowMs, cfg.LostGraceMs = r.VoteWindowMs, r.LostGraceMs
	cfg.PoseConfirmMs = map[string]int{"five": r.FiveConfirmMs, "middle": r.MiddleConfirmMs, "middle_ring_pinky": r.MiddleRingPinkyConfirmMs}
	return cfg
}

type Configuration struct {
	Version     int                 `json:"version"`
	Preferences Preferences         `json:"preferences"`
	Recognition RecognitionSettings `json:"recognition"`
	Gestures    []string            `json:"gestures"`
	Bindings    []engine.Binding    `json:"bindings"`
}

type Snapshot struct {
	Config   Configuration
	Revision uint64
	Path     string
}

func DefaultConfiguration(p Preferences) Configuration {
	return Configuration{Version: ConfigurationVersion, Preferences: p, Recognition: DefaultRecognition(), Gestures: append([]string(nil), canonicalGestures...), Bindings: DefaultBindings()}
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for key, value := range x {
			out[key] = cloneValue(value)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for n, value := range x {
			out[n] = cloneValue(value)
		}
		return out
	default:
		return v
	}
}

func (c Configuration) Clone() Configuration {
	c.Gestures = append([]string(nil), c.Gestures...)
	if c.Bindings != nil {
		bindings := make([]engine.Binding, len(c.Bindings))
		for n, b := range c.Bindings {
			bindings[n] = b
			bindings[n].ReleaseTo = append([]string(nil), b.ReleaseTo...)
			if b.Action.Params != nil {
				bindings[n].Action.Params = cloneValue(b.Action.Params).(map[string]any)
			}
		}
		c.Bindings = bindings
	}
	return c
}

// GestureCatalog is independent of bindings, including when every action is off.
func GestureCatalog() []gesture.PoseDef {
	var out []gesture.PoseDef
	for _, id := range canonicalGestures {
		for _, pose := range gesture.BuiltinPoses() {
			if pose.ID == id {
				out = append(out, pose)
				break
			}
		}
	}
	return append(out, gesture.PoseDef{ID: "two_index", Name: "双食指", Desc: "左右手同时只伸食指；距离变化可用于缩放", Fingers: [5]string{gesture.Any, gesture.Open, gesture.Closed, gesture.Closed, gesture.Closed}})
}

func hasGesture(id string) bool {
	if id == "two_index" {
		return true
	}
	for _, known := range canonicalGestures {
		if id == known {
			return true
		}
	}
	return false
}

func ActionCompatible(hand, pose, action string) bool {
	both := hand == "both"
	if both != (pose == "two_index") {
		return false
	}
	return engine.SupportsInput(action, both)
}

func (c *Configuration) Normalize() error {
	if c.Version != ConfigurationVersion {
		return fmt.Errorf("不支持配置版本 %d，需要版本 %d", c.Version, ConfigurationVersion)
	}
	if err := c.Preferences.Validate(); err != nil {
		return err
	}
	if err := c.Recognition.validate(); err != nil {
		return err
	}
	if len(c.Gestures) != len(canonicalGestures) {
		return fmt.Errorf("首版识别集合须保留全部 %d 个基础手势", len(canonicalGestures))
	}
	seenGestures := map[string]bool{}
	for _, id := range c.Gestures {
		if id == "two_index" || !hasGesture(id) || seenGestures[id] {
			return fmt.Errorf("识别集合含未知或重复手势 %q", id)
		}
		seenGestures[id] = true
	}
	if c.Bindings == nil {
		return fmt.Errorf("绑定列表不能为空值；停用全部动作请使用空列表 []")
	}
	if len(c.Bindings) > 256 {
		return fmt.Errorf("最多保存 256 条绑定")
	}
	seenIDs, seenInputs := map[string]bool{}, map[string]string{}
	for n := range c.Bindings {
		b := &c.Bindings[n]
		for _, spec := range engine.Catalog() {
			if spec.Type != b.Action.Type {
				continue
			}
			known := make(map[string]bool, len(spec.Params))
			for _, parameter := range spec.Params {
				known[parameter.Key] = true
			}
			for key := range b.Action.Params {
				if !known[key] {
					return fmt.Errorf("第 %d 条绑定「%s」含未知动作参数 %q", n+1, b.Name, key)
				}
			}
			break
		}
		if err := b.Normalize(hasGesture); err != nil {
			return fmt.Errorf("第 %d 条绑定「%s」：%w", n+1, b.Name, err)
		}
		if seenIDs[b.ID] {
			return fmt.Errorf("第 %d 条绑定 ID %q 重复", n+1, b.ID)
		}
		seenIDs[b.ID] = true
		if !ActionCompatible(b.Hand, b.Pose, b.Action.Type) {
			return fmt.Errorf("绑定「%s」的手别、手势和动作不兼容", b.Name)
		}
		if b.Hand == "both" && len(b.ReleaseTo) != 0 {
			return fmt.Errorf("双手绑定「%s」不能设置单手返回保护", b.Name)
		}
		for _, spec := range engine.Catalog() {
			if spec.Type == b.Action.Type && spec.NeedsAllowOpen && b.Enabled {
				return fmt.Errorf("绑定「%s」使用了尚未启用的外部程序动作", b.Name)
			}
		}
		if b.Enabled {
			key := b.Hand + "/" + b.Pose + "/" + b.Trigger
			if previous, exists := seenInputs[key]; exists {
				return fmt.Errorf("绑定「%s」与「%s」使用相同的手别、手势和触发方式", b.Name, previous)
			}
			seenInputs[key] = b.Name
		}
	}
	return nil
}

func decodeStrict(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("配置只能包含一个 JSON 对象")
	}
	return nil
}

func decodeConfiguration(data []byte) (Configuration, error) {
	if len(data) > maxConfigurationSize {
		return Configuration{}, fmt.Errorf("配置文件不能超过 1 MiB")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return Configuration{}, fmt.Errorf("读取配置 JSON：%w", err)
	}
	if object == nil {
		return Configuration{}, fmt.Errorf("配置须为 JSON 对象")
	}
	if rawVersion, exists := object["version"]; exists {
		var version int
		if err := json.Unmarshal(rawVersion, &version); err != nil || version < 2 || version > ConfigurationVersion {
			return Configuration{}, fmt.Errorf("不支持配置版本，需要版本 %d", ConfigurationVersion)
		}
		if raw, ok := object["bindings"]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Configuration{}, fmt.Errorf("配置缺少 bindings 列表；停用全部请使用 []")
		}
		if version == 2 {
			migrated, err := migrateV2(data)
			if err != nil {
				return Configuration{}, fmt.Errorf("升级版本 2 配置：%w", err)
			}
			data = migrated
		}
		cfg := DefaultConfiguration(DefaultPreferences())
		// Decoding a list over the default one would fill each stored binding
		// with the parameters of the default binding in the same place.
		cfg.Bindings = nil
		if err := decodeStrict(data, &cfg); err != nil {
			return Configuration{}, fmt.Errorf("读取配置：%w", err)
		}
		return cfg, cfg.Normalize()
	}
	p := DefaultPreferences()
	if err := decodeStrict(data, &p); err != nil {
		return Configuration{}, fmt.Errorf("读取旧版设置：%w", err)
	}
	cfg := DefaultConfiguration(p)
	return cfg, cfg.Normalize()
}

// migrateV2 rewrites a version 2 configuration as version 3: OK becomes
// middle_ring_pinky, and recognition times still at their old defaults take
// the new ones.
func migrateV2(data []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var object map[string]any
	if err := d.Decode(&object); err != nil {
		return nil, err
	}
	object["version"] = ConfigurationVersion
	if r, ok := object["recognition"].(map[string]any); ok {
		if v, ok := r["okConfirmMs"]; ok {
			delete(r, "okConfirmMs")
			r["middleRingPinkyConfirmMs"] = v
		}
		for key, old := range map[string]json.Number{"fiveConfirmMs": "30", "lostGraceMs": "200"} {
			if r[key] == old {
				delete(r, key)
			}
		}
	}
	renameOK := func(list []any) {
		for n, v := range list {
			if v == "ok" {
				list[n] = "middle_ring_pinky"
			}
		}
	}
	if gestures, ok := object["gestures"].([]any); ok {
		renameOK(gestures)
	}
	bindings, _ := object["bindings"].([]any)
	for _, item := range bindings {
		b, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if b["pose"] == "ok" {
			b["pose"] = "middle_ring_pinky"
			if b["name"] == "右手 OK 右 Alt" {
				b["name"] = "右手后三指 右 Alt"
			}
		}
		if returns, ok := b["releaseTo"].([]any); ok {
			renameOK(returns)
		}
	}
	return json.Marshal(object)
}

// storedVersion is the version of a stored configuration: 1 for the first
// format, which had none, and 0 when data is not a configuration object.
func storedVersion(data []byte) int {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return 0
	}
	raw, ok := object["version"]
	if !ok {
		return 1
	}
	var version int
	if json.Unmarshal(raw, &version) != nil {
		return 0
	}
	return version
}

func LoadConfiguration(path string) (Configuration, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfiguration(DefaultPreferences())
		return cfg, cfg.Normalize()
	}
	if err != nil {
		return Configuration{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigurationSize+1))
	if err != nil {
		return Configuration{}, err
	}
	return decodeConfiguration(data)
}

func SaveConfiguration(path string, cfg Configuration) error {
	cfg = cfg.Clone()
	if err := cfg.Normalize(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxConfigurationSize {
		return fmt.Errorf("配置文件不能超过 1 MiB")
	}
	var previous []byte
	if oldFile, openErr := os.Open(path); openErr == nil {
		previous, _ = io.ReadAll(io.LimitReader(oldFile, maxConfigurationSize+1))
		_ = oldFile.Close()
	}
	if len(previous) > 0 && len(previous) <= maxConfigurationSize {
		if old := storedVersion(previous); old > 0 && old < ConfigurationVersion {
			if _, err := decodeConfiguration(previous); err == nil {
				name := fmt.Sprintf("%s.v%d.bak", path, old)
				backup, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil && !os.IsExist(err) {
					return fmt.Errorf("备份旧配置：%w", err)
				}
				if err == nil {
					_, writeErr := backup.Write(previous)
					if writeErr == nil {
						writeErr = backup.Sync()
					}
					closeErr := backup.Close()
					if writeErr != nil || closeErr != nil {
						_ = os.Remove(name)
						return fmt.Errorf("备份旧配置失败：%v %v", writeErr, closeErr)
					}
				}
			}
		}
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mouse-config-*.tmp")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
