package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// UserConfigDir on Windows uses APPDATA; on Linux XDG_CONFIG_HOME.
	// Force path via writing to known location through Save after overriding Path is hard;
	// instead write/read using the resolved path under temp by setting HOME/XDG.
	home := filepath.Join(dir, "home")
	_ = os.MkdirAll(home, 0o755)
	t.Setenv("HOME", home)

	cfg := Config{LastBaseDir: `/captures`, AutoRunEnabled: true, IdleTimeout: "90s"}
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.LastBaseDir != cfg.LastBaseDir || !got.AutoRunEnabled || got.IdleTimeout != "90s" {
		t.Fatalf("got %+v", got)
	}
	if got.IdleDuration() != 90*time.Second {
		t.Fatalf("duration %v", got.IdleDuration())
	}
}

func TestDefaultIdle(t *testing.T) {
	d := Default()
	if d.AutoRunEnabled || d.IdleDuration() != 3*time.Minute {
		t.Fatalf("%+v", d)
	}
}
