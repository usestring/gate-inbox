package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestHogsDefaultToTheDocumentedTable(t *testing.T) {
	cfg, err := LoadDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hogs
	if !h.On() || h.SampleEvery.Duration != 10*time.Second || h.ResetAfter.Duration != 2*time.Minute || h.Cooldown.Duration != 30*time.Minute {
		t.Fatalf("hogs = %+v", h)
	}
	cpu, memory := DefaultHogTiers()
	if !reflect.DeepEqual(h.CPU, cpu) || !reflect.DeepEqual(h.Memory, memory) {
		t.Fatalf("tiers = %+v / %+v, want the built-in table", h.CPU, h.Memory)
	}
}

func TestHogsKeepWhatTheFileSaysAndFillTheRest(t *testing.T) {
	dir := t.TempDir()
	body := `
[hogs]
cooldown = "1h"

[hogs.cpu]
notice = [{ percent = 150, for = "15m" }]
stop = []

[hogs.memory]
warn = [{ gib = 12, available_below = 30, for = "1m" }]
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := cfg.Hogs
	cpu, memory := DefaultHogTiers()
	if h.Cooldown.Duration != time.Hour || h.ResetAfter.Duration != 2*time.Minute {
		t.Fatalf("durations = %+v", h)
	}
	if len(h.CPU.Notice) != 1 || h.CPU.Notice[0].Percent != 150 || h.CPU.Notice[0].For.Duration != 15*time.Minute {
		t.Fatalf("cpu notice = %+v", h.CPU.Notice)
	}
	if !reflect.DeepEqual(h.CPU.Warn, cpu.Warn) {
		t.Fatalf("an unwritten tier did not take the built-in rules: %+v", h.CPU.Warn)
	}
	if h.CPU.Stop == nil || len(h.CPU.Stop) != 0 {
		t.Fatalf("stop = [] should switch the tier off, got %+v", h.CPU.Stop)
	}
	if want := (HogRule{GiB: 12, AvailableBelow: 30, For: Duration{Duration: time.Minute}}); len(h.Memory.Warn) != 1 || h.Memory.Warn[0] != want {
		t.Fatalf("memory warn = %+v", h.Memory.Warn)
	}
	if !reflect.DeepEqual(h.Memory.Stop, memory.Stop) {
		t.Fatalf("memory stop = %+v", h.Memory.Stop)
	}
}

func TestHogsCanBeSwitchedOff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[hogs]\nenabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hogs.On() {
		t.Fatal("enabled = false left hog detection on")
	}
}
