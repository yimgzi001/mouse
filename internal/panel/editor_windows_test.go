//go:build windows && amd64

package panel

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gesturectl/internal/control"
)

func TestDraftKeepsUnsavedChangesAndRejectsStaleApply(t *testing.T) {
	initial := control.Snapshot{Config: control.DefaultConfiguration(control.DefaultPreferences()), Revision: 1}
	s := newDraft(initial)
	s.config.Bindings[0].Name = "未保存名称"
	changed := initial
	changed.Revision = 2
	changed.Config = initial.Config.Clone()
	changed.Config.Preferences.MoveLeft = .8
	if s.observe(changed) {
		t.Fatal("dirty draft was replaced by external settings")
	}
	if s.revision != 1 || s.config.Bindings[0].Name != "未保存名称" || !s.stale {
		t.Fatal("draft or original revision lost")
	}
	err := s.apply(func(rev uint64, cfg control.Configuration) (control.Snapshot, error) {
		if rev != 1 {
			t.Fatal("wrong revision")
		}
		return control.Snapshot{}, errors.New("配置已更新")
	})
	if err == nil || !s.dirty() || s.config.Bindings[0].Name != "未保存名称" {
		t.Fatal("failed apply discarded draft")
	}
}

func TestDraftImportExportAndSeparateResets(t *testing.T) {
	cfg := control.DefaultConfiguration(control.DefaultPreferences())
	cfg.Preferences.MoveLeft = .8
	cfg.Preferences.SwapHands = true
	s := newDraft(control.Snapshot{Config: cfg, Revision: 3})
	s.config.Bindings[0].Enabled = false
	path := filepath.Join(t.TempDir(), "draft.json")
	if err := s.export(path); err != nil {
		t.Fatal(err)
	}
	saved, err := control.LoadConfiguration(path)
	if err != nil || saved.Bindings[0].Enabled {
		t.Fatalf("export: %v", err)
	}
	s.config.Bindings = nil
	if err := s.importFile(path); err != nil {
		t.Fatal(err)
	}
	if s.revision != 3 || s.config.Bindings[0].Enabled || !s.dirty() {
		t.Fatal("import must only update draft")
	}
	s.resetBindings()
	if s.config.Preferences.MoveLeft != .8 || !s.config.Preferences.SwapHands {
		t.Fatal("mapping reset changed preferences")
	}
	s.resetPreferences()
	if s.config.Preferences.MoveLeft != control.DefaultPreferences().MoveLeft || !s.config.Preferences.SwapHands {
		t.Fatal("sensitivity reset changed hand correction")
	}
}

func TestCleanDraftTracksExternalSettings(t *testing.T) {
	cfg := control.DefaultConfiguration(control.DefaultPreferences())
	s := newDraft(control.Snapshot{Config: cfg, Revision: 1})
	next := control.Snapshot{Config: cfg.Clone(), Revision: 2}
	next.Config.Preferences.Zoom = 2
	if !s.observe(next) || s.config.Preferences.Zoom != 2 || s.dirty() {
		t.Fatal("clean draft did not refresh")
	}
}

func TestImportMissingAndOversizedFilesKeepDraft(t *testing.T) {
	s := newDraft(control.Snapshot{Config: control.DefaultConfiguration(control.DefaultPreferences()), Revision: 1})
	s.config.Bindings[0].Name = "保留草稿"
	if err := s.importFile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing import silently restored defaults")
	}
	path := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.importFile(path); err == nil {
		t.Fatal("oversized import accepted")
	}
	if s.config.Bindings[0].Name != "保留草稿" || s.revision != 1 {
		t.Fatal("failed import discarded draft")
	}
}
