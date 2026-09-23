package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Absent means on, so a config written before the section existed keeps both providers.
func TestIntegrationsDefaultOnAndSwitchOff(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Integrations.GitHub.On() || !cfg.Integrations.Linear.On() {
		t.Fatalf("default integrations = %+v, want both on", cfg.Integrations)
	}

	dir := t.TempDir()
	body := "[integrations.github]\nenabled = false\n\n[integrations.linear]\nenabled = true\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if cfg.Integrations.GitHub.On() {
		t.Error("github is on after enabled = false")
	}
	if !cfg.Integrations.Linear.On() {
		t.Error("linear is off after enabled = true")
	}
}
