package ui

import (
	"strings"
	"testing"
)

func TestDeviceThemesPersistAcrossHandoffs(t *testing.T) {
	m := buildModel(t)
	t.Cleanup(func() { applyTheme(themes[0]) })
	if err := m.store.SetSetting(themeSetting, "classic"); err != nil {
		t.Fatal(err)
	}
	m.loadDeviceTheme("device:ipad")
	m.openSettings()
	m.settings.field = settingsFieldTheme
	m.settings.themeIndex = themeIndex("oled") - 1
	m.cycleSetting(1)
	m.loadDeviceTheme("device:laptop")
	if current.Name != "classic" {
		t.Fatalf("new device theme = %q", current.Name)
	}
	if m.settings.themeIndex != themeIndex("classic") {
		t.Fatal("settings retained previous device's theme")
	}
	m.settings.themeIndex = themeIndex("paper") - 1
	m.cycleSetting(1)
	m.persistSettings()
	if shared := storedTheme(m.store); shared != "classic" {
		t.Fatalf("device changed shared default to %q", shared)
	}
	m.loadDeviceTheme("device:ipad")
	if current.Name != "oled" {
		t.Fatalf("tablet theme = %q", current.Name)
	}
	m.loadDeviceTheme("device:laptop")
	if current.Name != "paper" {
		t.Fatalf("laptop theme = %q", current.Name)
	}
	if !strings.Contains(m.viewSettings(), "device:laptop") {
		t.Fatal("settings hide the theme's device")
	}
	m.themeDevice = ""
	m.loadDeviceTheme("device:ipad")
	if current.Name != "oled" {
		t.Fatalf("reconnected tablet theme = %q", current.Name)
	}
}

func TestMissingClientKeepsDeviceTheme(t *testing.T) {
	m := buildModel(t)
	t.Cleanup(func() { applyTheme(themes[0]) })
	m.loadDeviceTheme("device:ipad")
	applyTheme(themes[themeIndex("oled")])
	m.lastFrame = "old frame"
	m.loadDeviceTheme("")
	if m.themeDevice != "device:ipad" || current.Name != "oled" {
		t.Fatal("detach changed the device theme")
	}
	m.loadDeviceTheme("device:laptop")
	if m.lastFrame != "" {
		t.Fatal("device handoff reused a frame from the previous theme")
	}
}

func TestDirectDeviceThemeLoadsOnStartup(t *testing.T) {
	m := buildModel(t)
	t.Cleanup(func() { applyTheme(themes[0]) })
	t.Setenv("GATE_INBOX_DEVICE", "tablet")
	if err := m.store.SetSetting("theme:device:tablet", "oled"); err != nil {
		t.Fatal(err)
	}
	m.ownPane = ""
	m.initDeviceTheme()
	if current.Name != "oled" || m.themeDevice != "device:tablet" {
		t.Fatal("direct device theme was not restored")
	}
	m.themeDevice = ""
	m.ownPane = "%1"
	m.initDeviceTheme()
	if m.themeDevice != "" {
		t.Fatal("tmux used its stale launch environment")
	}
}
