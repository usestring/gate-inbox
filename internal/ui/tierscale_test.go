package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/priority"
)

func TestPriorityScaleDefaultsToTwoRaisedOneLowered(t *testing.T) {
	m := buildModel(t)
	if got := m.scale(); got != priority.DefaultScale {
		t.Fatalf("fresh board scale = %+v, want %+v", got, priority.DefaultScale)
	}
	if got := storedPriorityScale(m.store); got != "+2/-1" {
		t.Fatalf("stored default = %q, want +2/-1", got)
	}
}

func TestPriorityScaleSettingRoundTripsAndFailsSafe(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.settings.field = settingsFieldPriorityScale
	m.applyCmd(t, m.cycleSetting(1))
	if m.settings.priorityScale != "+2/-2" {
		t.Fatalf("one step from +2/-1 landed on %q, want +2/-2", m.settings.priorityScale)
	}
	if _, cmd := m.saveAndCloseSettings(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := m.scale(); got != (priority.Scale{Raised: 2, Lowered: 2}) {
		t.Fatalf("saved scale = %+v", got)
	}
	if got := storedPriorityScale(m.store); got != "+2/-2" {
		t.Fatalf("stored %q, want +2/-2", got)
	}
	for _, bad := range []string{"+4/-1", "+0/-1", "2/1", "lots"} {
		if err := m.store.SetSetting(priorityScaleSetting, bad); err != nil {
			t.Fatal(err)
		}
		if got := storedPriorityScale(m.store); got != "+2/-1" {
			t.Errorf("stored %q read as %q, want the default +2/-1", bad, got)
		}
	}
}

func TestPriorityScaleModesCoverEveryCombination(t *testing.T) {
	if len(priorityScaleModes) != 3*4 {
		t.Fatalf("modes = %v", priorityScaleModes)
	}
	for _, mode := range priorityScaleModes {
		if normalizePriorityScale(mode) != mode {
			t.Fatalf("mode %q does not round-trip", mode)
		}
	}
}

// The key cycles only the chosen tiers, and a tier stored past the scale
// paints and sorts as its edge.
func TestPriorityKeyCyclesTheConfiguredScale(t *testing.T) {
	m := buildModel(t)
	m.priorityScale = "+2/-1"
	tier := priority.Unset
	var seen []priority.Tier
	for range 4 {
		tier = m.scale().Next(tier)
		seen = append(seen, tier)
	}
	want := []priority.Tier{priority.High, priority.Medium, priority.Low, priority.Unset}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("cycle = %v, want %v", seen, want)
		}
	}
	m.priorityScale = "+3/-3"
	if got := m.scale().Next(priority.Unset); got != priority.Urgent {
		t.Fatalf("+3/-3 starts at %q, want urgent", got)
	}
	if got := priorityLegend(priority.DefaultScale, priority.Urgent); got != "priority medium" {
		t.Fatalf("legend from a stored urgent on +2/-1 = %q", got)
	}
}
