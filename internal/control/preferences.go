package control

import (
	"fmt"
	"math"

	"gesturectl/internal/engine"
	"gesturectl/internal/gesture"
)

type Preferences struct {
	MoveLeft  float64 `json:"moveLeft"`
	MoveRight float64 `json:"moveRight"`
	Drag      float64 `json:"drag"`
	Scroll    float64 `json:"scroll"`
	Switcher  float64 `json:"switcher"`
	Zoom      float64 `json:"zoom"`
	SwapHands bool    `json:"swapHands"`
}

func (p Preferences) recognizerSettings() gesture.Config {
	cfg := gesture.DefaultConfig()
	cfg.PoseConfirmMs = map[string]int{"five": 30, "middle": 60, "ok": 80}
	cfg.SwipeWindowMs = 250
	cfg.SwipeDistance = .65 * 2.5 / p.Switcher
	cfg.SwipeCooldownMs = 350
	return cfg
}

func DefaultPreferences() Preferences {
	return Preferences{MoveLeft: .5, MoveRight: .5, Drag: .5, Scroll: 1, Switcher: 2.5, Zoom: 1}
}
func (p Preferences) Validate() error {
	for name, value := range map[string]float64{"moveLeft": p.MoveLeft, "moveRight": p.MoveRight, "drag": p.Drag, "scroll": p.Scroll, "switcher": p.Switcher, "zoom": p.Zoom} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < .1 || value > 5 {
			return fmt.Errorf("%s 须在 0.1～5 之间", name)
		}
	}
	return nil
}
func LoadPreferences(path string) (Preferences, error) {
	cfg, err := LoadConfiguration(path)
	return cfg.Preferences, err
}
func SavePreferences(path string, p Preferences) error {
	if err := p.Validate(); err != nil {
		return err
	}
	cfg, err := LoadConfiguration(path)
	if err != nil {
		return err
	}
	cfg.Preferences = p
	return SaveConfiguration(path, cfg)
}
func (p Preferences) engineSettings() engine.Settings {
	s := engine.DefaultSettings()
	s.Pointer.LeftSensitivity, s.Pointer.RightSensitivity, s.Pointer.DragSensitivity = p.MoveLeft, p.MoveRight, p.Drag
	s.Scroll.Speed *= p.Scroll
	s.Scroll.ZoomSpeed *= p.Zoom
	s.Switcher.StepDistance /= p.Switcher
	s.Switcher.MinStepMs = 70
	s.PairZoom = engine.PairZoomSettings{Enabled: true, Sensitivity: p.Zoom, ConfirmMs: 100}
	return s
}
