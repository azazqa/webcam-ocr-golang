// Package config loads and saves user preferences under UserConfigDir.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const appName = "webcam-ocr"

// Config is persisted application settings.
type Config struct {
	LastBaseDir    string `json:"lastBaseDir"`
	AutoRunEnabled bool   `json:"autoRunEnabled"`
	IdleTimeout    string `json:"idleTimeout"` // e.g. "3m"
}

// Default returns defaults (auto-run off, idle 3 minutes).
func Default() Config {
	return Config{
		AutoRunEnabled: false,
		IdleTimeout:    "3m",
	}
}

// Path returns %APPDATA%\webcam-ocr\config.json (or OS equivalent).
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appName, "config.json"), nil
}

// Load reads config from disk. Missing file returns Default().
func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Default(), err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Default(), err
	}
	cfg := Default()
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Default(), fmt.Errorf("config: %w", err)
	}
	if cfg.IdleTimeout == "" {
		cfg.IdleTimeout = "3m"
	}
	return cfg, nil
}

// Save writes config to disk.
func Save(cfg Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// IdleDuration parses IdleTimeout; invalid values fall back to 3m.
func (c Config) IdleDuration() time.Duration {
	d, err := time.ParseDuration(c.IdleTimeout)
	if err != nil || d <= 0 {
		return 3 * time.Minute
	}
	return d
}
