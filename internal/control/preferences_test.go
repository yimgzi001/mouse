package control

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPreferencesPersistIndependently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	p, err := LoadPreferences(path)
	if err != nil {
		t.Fatal(err)
	}
	p.MoveLeft = .3
	p.MoveRight = .8
	p.Drag = .4
	p.Scroll = 1.7
	p.Switcher = 3.2
	p.Zoom = .6
	if err = SavePreferences(path, p); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPreferences(path)
	if err != nil || got != p {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	p.Scroll = 2.1
	if err = SavePreferences(path, p); err != nil {
		t.Fatal(err)
	}
	got, err = LoadPreferences(path)
	if err != nil || got != p {
		t.Fatalf("overwrite: %+v %v", got, err)
	}
}
func TestInvalidPreferencesCannotOverwriteGoodSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	good := DefaultPreferences()
	if err := SavePreferences(path, good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1), 5.1} {
		p := good
		p.Zoom = bad
		if err := SavePreferences(path, p); err == nil {
			t.Fatalf("accepted zoom %v", bad)
		}
	}
	got, err := LoadPreferences(path)
	if err != nil || got != good {
		t.Fatalf("damaged saved preferences: %+v %v", got, err)
	}
}
func TestPartialAndInvalidPreferencesFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"moveLeft":0.4}`), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPreferences(path)
	if err != nil || p.MoveLeft != .4 || p.MoveRight != .5 {
		t.Fatalf("partial: %+v %v", p, err)
	}
	for _, data := range []string{`{"zoom":0}`, `{"zoom":1} {}`, `{"unknown":1}`} {
		os.WriteFile(path, []byte(data), 0600)
		if _, err := LoadPreferences(path); err == nil {
			t.Fatalf("accepted invalid settings: %s", data)
		}
	}
}
