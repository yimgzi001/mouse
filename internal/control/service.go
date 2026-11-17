package control

import (
	"fmt"
	"sync"

	"gesturectl/internal/engine"
)

type Service struct {
	mu         sync.Mutex
	path       string
	config     Configuration
	revision   uint64
	controller *Controller
	save       func(string, Configuration) error
}

func NewService(path string, cfg Configuration, inj engine.Injector) (*Service, error) {
	prepared, err := NewWithConfiguration(inj, cfg)
	if err != nil {
		return nil, err
	}
	cfg = prepared.Configuration()
	if err := SaveConfiguration(path, cfg); err != nil {
		return nil, fmt.Errorf("保存初始配置：%w", err)
	}
	return &Service{path: path, config: cfg, revision: 1, controller: prepared, save: SaveConfiguration}, nil
}

func (s *Service) Controller() *Controller { return s.controller }

func (s *Service) snapshotLocked() Snapshot {
	return Snapshot{Config: s.config.Clone(), Revision: s.revision, Path: s.path}
}

func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Service) applyLocked(expected uint64, cfg Configuration) (Snapshot, error) {
	if expected != s.revision {
		return s.snapshotLocked(), fmt.Errorf("配置已由其他入口修改，请重新载入后再保存（草稿未丢失）")
	}
	prepared, err := NewWithConfiguration(s.controller.injector, cfg)
	if err != nil {
		return s.snapshotLocked(), err
	}
	normalized := prepared.Configuration()
	if err := s.save(s.path, normalized); err != nil {
		return s.snapshotLocked(), fmt.Errorf("配置未保存，仍使用原设置：%w", err)
	}
	s.controller.applyPrepared(prepared)
	s.config = normalized
	s.revision++
	return s.snapshotLocked(), nil
}

func (s *Service) Apply(expected uint64, cfg Configuration) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(expected, cfg)
}

// Preferences-only callers merge into the latest bindings, never a fresh preset.
// Hand calibration is changed by ToggleHands or a revision-checked full draft.
func (s *Service) UpdatePreferences(p Preferences) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.SwapHands = s.config.Preferences.SwapHands
	cfg := s.config.Clone()
	cfg.Preferences = p
	return s.applyLocked(s.revision, cfg)
}

// PatchPreferences prevents a stale preview slider from overwriting unrelated
// values edited in the panel while that preview was visible.
func (s *Service) PatchPreferences(base, next Preferences) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.config.Preferences
	for _, change := range []struct {
		old, value float64
		target     *float64
	}{
		{base.MoveLeft, next.MoveLeft, &p.MoveLeft}, {base.MoveRight, next.MoveRight, &p.MoveRight},
		{base.Drag, next.Drag, &p.Drag}, {base.Scroll, next.Scroll, &p.Scroll},
		{base.Switcher, next.Switcher, &p.Switcher}, {base.Zoom, next.Zoom, &p.Zoom},
	} {
		if change.old != change.value {
			*change.target = change.value
		}
	}
	cfg := s.config.Clone()
	cfg.Preferences = p
	return s.applyLocked(s.revision, cfg)
}

func (s *Service) ToggleHands() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.config.Clone()
	cfg.Preferences.SwapHands = !cfg.Preferences.SwapHands
	return s.applyLocked(s.revision, cfg)
}
