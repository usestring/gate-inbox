package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// Stepping the hibernation rows and saving puts both in force on the board
// and in the store, and stepping back to config.toml's values clears the
// override.
func TestSettingsHibernationRowsPersist(t *testing.T) {
	m := buildModel(t)
	if m.hibernateAfter != 0 || m.hibernateMinSessions != 0 {
		t.Fatalf("hibernation = (%s, %d), want off by default", m.hibernateAfter, m.hibernateMinSessions)
	}
	m.openSettings()
	m.settings.field = settingsFieldHibernateAfter
	m.handleSettingsKey(key("right"))
	m.handleSettingsKey(key("right"))
	m.settings.field = settingsFieldHibernateMin
	m.handleSettingsKey(key("right"))
	m.handleSettingsKey(key("esc"))
	if m.hibernateAfter != time.Hour || m.hibernateMinSessions != 5 {
		t.Fatalf("hibernation = (%s, %d), want (1h, 5)", m.hibernateAfter, m.hibernateMinSessions)
	}
	if after, minSessions := storedHibernation(m.store, m.cfg); after != time.Hour || minSessions != 5 {
		t.Fatalf("stored hibernation = (%s, %d), want (1h, 5)", after, minSessions)
	}

	m.openSettings()
	m.settings.field = settingsFieldHibernateAfter
	m.handleSettingsKey(key("left"))
	m.handleSettingsKey(key("left"))
	m.settings.field = settingsFieldHibernateMin
	m.handleSettingsKey(key("left"))
	m.handleSettingsKey(key("esc"))
	for _, setting := range []string{store.HibernateAfterSetting, store.HibernateMinSessionsSetting} {
		if raw, err := m.store.Setting(setting); err != nil || raw != "" {
			t.Fatalf("%s = %q err=%v, want cleared back to the config default", setting, raw, err)
		}
	}
}

// A config value off the preset list stays reachable from the row.
func TestHibernateChoicesKeepAnOffListValue(t *testing.T) {
	if got := stepChoice(hibernateAfterChoices, 90*time.Minute, 1); got != 2*time.Hour {
		t.Fatalf("step from 90m = %s, want 2h", got)
	}
	if got := stepChoice(hibernateAfterChoices, 2*time.Hour, -1); got != time.Hour {
		t.Fatalf("step back from 2h = %s, want 1h", got)
	}
	if got := shortDuration(72 * time.Hour); got != "3d" {
		t.Fatalf("shortDuration(72h) = %q", got)
	}
}
