package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// never means never: the footer goes on every terminal, including the tall
// one the height tiers would have given the full legend, and including focus
// mode, where the height tiers keep a row back whatever the height.
func TestChromeNeverDropsTheLegendAtEveryHeight(t *testing.T) {
	for _, tc := range []struct {
		height int
		mode   mode
	}{
		{50, modeList},
		{50, modeFocus},
		{13, modeFocus},
	} {
		m := fleetModel(t, 6, 45, tc.height)
		m.mode, m.chrome = tc.mode, chromeNever
		if got := m.legendRows(); got != 0 {
			t.Errorf("height %d mode %d: %d legend rows under %q, want 0", tc.height, tc.mode, got, chromeNever)
		}
	}
}

// always is the other end of the same override: the full legend on a
// terminal the tiers would have cut it to one row on, or dropped it from.
func TestChromeAlwaysKeepsTheFullLegend(t *testing.T) {
	for _, height := range []int{50, 27, 13} {
		m := fleetModel(t, 6, 45, height)
		m.chrome = chromeAlways
		if got := m.legendRows(); got != legendMaxRows {
			t.Errorf("height %d: %d legend rows under %q, want %d", height, got, chromeAlways, legendMaxRows)
		}
	}
}

// auto is the height tiers untouched, and an unset setting is auto: a store
// written before this setting existed must not change what the frame does.
func TestChromeAutoLeavesTheHeightTiersAlone(t *testing.T) {
	for _, chrome := range []string{chromeAuto, ""} {
		for _, tc := range []struct {
			height, want int
		}{
			{50, legendMaxRows},
			{27, 1},
			{13, 0},
		} {
			m := fleetModel(t, 6, 45, tc.height)
			m.chrome = chrome
			if got := m.legendRows(); got != tc.want {
				t.Errorf("chrome %q height %d: %d legend rows, want %d", chrome, tc.height, got, tc.want)
			}
		}
	}
}

// The rows a dropped footer gives up go to the body rather than being spent
// on an empty line, which is what lipgloss would count them as.
func TestChromeNeverGivesTheFooterRowsToTheBody(t *testing.T) {
	m := fleetModel(t, 87, 45, 50)
	tall := m.listBodyHeight()
	m.chrome = chromeNever
	if rows := m.footerRows(); rows != 0 {
		t.Fatalf("footer takes %d rows with the legend turned off", rows)
	}
	if got := m.listBodyHeight(); got <= tall {
		t.Errorf("body kept %d rows with the legend off, %d with it on; it should have grown", got, tall)
	}
}

// The choice survives the settings panel: cycled with →, saved with esc,
// read back off the store the next start reads.
func TestChromeSettingRoundTripsThroughThePanel(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.settings.field = settingsFieldChrome
	m.applyCmd(t, m.cycleSetting(1))
	if m.settings.chrome != chromeAlways {
		t.Fatalf("one step from %q landed on %q, want %q", chromeAuto, m.settings.chrome, chromeAlways)
	}
	m.applyCmd(t, m.cycleSetting(1))
	if _, cmd := m.saveAndCloseSettings(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.chrome != chromeNever {
		t.Errorf("model chrome %q after saving, want %q", m.chrome, chromeNever)
	}
	if got := storedChrome(m.store); got != chromeNever {
		t.Errorf("stored chrome %q, want %q", got, chromeNever)
	}
}

// The key is the setting's two useful states under one press, and it persists
// the way the panel does: a footer put away stays away across a restart.
func TestChromeToggleKeyHidesAndRestoresTheFooter(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "chrometoggle", t.TempDir(), "")
	m.selectSessionRow(t, "chrometoggle")

	updated, _ := m.handleKey(key(","))
	*m = *updated.(*Model)
	if m.chrome != chromeNever {
		t.Fatalf("chrome %q after the first press, want %q (err %q)", m.chrome, chromeNever, m.errBar.text)
	}
	if got := storedChrome(m.store); got != chromeNever {
		t.Errorf("stored chrome %q, want %q", got, chromeNever)
	}

	updated, _ = m.handleKey(key(","))
	*m = *updated.(*Model)
	if m.chrome != chromeAuto {
		t.Fatalf("chrome %q after the second press, want %q", m.chrome, chromeAuto)
	}
	if got := storedChrome(m.store); got != chromeAuto {
		t.Errorf("stored chrome %q, want %q", got, chromeAuto)
	}
}

// Bringing the footer back gives the operator the mode they chose, not the
// height tiers: somebody on "always" asked for the full legend on a short
// terminal, and hiding it for a moment is not them changing their mind.
func TestChromeToggleRestoresTheModeItHid(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "chromealways", t.TempDir(), "")
	m.selectSessionRow(t, "chromealways")
	m.chrome = chromeAlways

	for i := 0; i < 2; i++ {
		updated, _ := m.handleKey(key(","))
		*m = *updated.(*Model)
	}
	if m.chrome != chromeAlways {
		t.Errorf("chrome %q after hiding and showing again, want %q", m.chrome, chromeAlways)
	}
}

// Focused, the footer is still the manager's row: alt+, puts it away without
// leaving the session, and the plain comma stays the agent's character.
func TestFocusAltCommaTogglesTheFooter(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "chromefocus", t.TempDir(), "")
	m.selectSessionRow(t, "chromefocus")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: ',', Mod: tea.ModAlt})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("alt+, left the pane, mode = %v", m.mode)
	}
	if m.chrome != chromeNever {
		t.Fatalf("chrome %q after alt+, in focus, want %q (err %q)", m.chrome, chromeNever, m.errBar.text)
	}
	if rows := m.legendRows(); rows != 0 {
		t.Errorf("focused footer kept %d rows with the legend toggled off", rows)
	}
}

// A store holding a value this build does not know reads as auto rather than
// as a mode nothing implements.
func TestUnknownStoredChromeReadsAsAuto(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting(chromeSetting, "dim"); err != nil {
		t.Fatal(err)
	}
	if got := storedChrome(m.store); got != chromeAuto {
		t.Errorf("stored %q read back as %q, want %q", "dim", got, chromeAuto)
	}
}
