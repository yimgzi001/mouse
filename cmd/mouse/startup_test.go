package main

import (
	"flag"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupKeepsTheCameraAndSettingsFlags(t *testing.T) {
	flags := flag.NewFlagSet("mouse", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Int("camera", 0, "")
	flags.Bool("verbose", false, "")
	flags.Bool("swap-hands", false, "")
	config := flags.String("config", "", "")
	if err := flags.Parse([]string{"-camera", "1", "-verbose", "-swap-hands", "-config", "my settings.json"}); err != nil {
		t.Fatal(err)
	}
	entry := startupEntry(flags, *config)
	if entry == nil {
		t.Fatal("no startup entry")
	}
	path, err := filepath.Abs("my settings.json")
	if err != nil {
		t.Fatal(err)
	}
	want := `" -background -camera=1 "-config=` + path + `" -swap-hands=true`
	if !strings.HasSuffix(entry.Command, want) {
		t.Fatalf("startup command %s, want it to end with %s", entry.Command, want)
	}
}
