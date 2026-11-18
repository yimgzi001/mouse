//go:build windows && amd64

package panel

import (
	"fmt"
	"os"
	"reflect"

	"gesturectl/internal/control"
)

// draft owns a deep copy of the live snapshot. A revision is only advanced by
// a successful apply or an explicit reload, so external edits cannot be lost.
type draft struct {
	config, baseline control.Configuration
	revision         uint64
	stale            bool
}

func newDraft(s control.Snapshot) *draft { d := &draft{}; d.replace(s); return d }
func (d *draft) replace(s control.Snapshot) {
	d.config = s.Config.Clone()
	d.baseline = s.Config.Clone()
	d.revision = s.Revision
	d.stale = false
}
func (d *draft) dirty() bool { return !reflect.DeepEqual(d.config, d.baseline) }
func (d *draft) observe(s control.Snapshot) bool {
	if s.Revision == d.revision {
		return false
	}
	if d.dirty() {
		d.stale = true
		return false
	}
	d.replace(s)
	return true
}
func (d *draft) apply(apply func(uint64, control.Configuration) (control.Snapshot, error)) error {
	next := d.config.Clone()
	if err := next.Normalize(); err != nil {
		return err
	}
	s, err := apply(d.revision, next)
	if err != nil {
		return err
	}
	d.replace(s)
	return nil
}
func (d *draft) resetBindings() {
	d.config.Bindings = control.DefaultConfiguration(d.config.Preferences).Bindings
}
func (d *draft) resetPreferences() {
	previous := d.config.Preferences
	d.config.Preferences = control.DefaultPreferences()
	d.config.Preferences.SwapHands, d.config.Preferences.HideHints = previous.SwapHands, previous.HideHints
}
func (d *draft) importFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return fmt.Errorf("导入文件须为不超过 1 MiB 的普通 JSON 文件")
	}
	cfg, err := control.LoadConfiguration(path)
	if err != nil {
		return err
	}
	if err = cfg.Normalize(); err != nil {
		return err
	}
	d.config = cfg.Clone()
	return nil
}
func (d *draft) export(path string) error {
	cfg := d.config.Clone()
	if err := cfg.Normalize(); err != nil {
		return err
	}
	return control.SaveConfiguration(path, cfg)
}
