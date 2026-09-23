package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The full dock keeps its nine rows only while the list beside it keeps eight
// and half the rail; a shorter rail folds it to a line, and one with no room
// for even that drops it.
func TestDockTierGivesWayBeforeTheList(t *testing.T) {
	const fullLines = 8
	for _, tc := range []struct {
		height int
		want   dockTier
	}{
		{44, dockFull},  // 40x50: list keeps 35
		{28, dockFull},  // 120x34: list keeps 19
		{23, dockFull},  // 45x27: list keeps 14, over half
		{17, dockBrief}, // list would keep 8 but under half
		{14, dockBrief}, // 45x20
		{9, dockBrief},  // 45x12
		{4, dockNone},
		{3, dockNone},
	} {
		if got := dockTierFor(tc.height, fullLines); got != tc.want {
			t.Errorf("height %d: tier %d, want %d", tc.height, got, tc.want)
		}
	}
}

// The legend spends three rows on a terminal with room, one on a short one,
// none on a very short one, and focus mode keeps the row that names the way
// back to the manager whatever the height.
func TestLegendRowsFollowTheHeight(t *testing.T) {
	for _, tc := range []struct {
		height int
		mode   mode
		layout string
		want   int
	}{
		{50, modeList, layoutAuto, legendMaxRows},
		{30, modeList, layoutAuto, legendMaxRows},
		// The phone with its keyboard up. A threshold that does not fire here
		// is a threshold that does nothing.
		{27, modeList, layoutAuto, 1},
		{14, modeList, layoutAuto, 1},
		{13, modeList, layoutAuto, 0},
		{13, modeFocus, layoutAuto, 1},
		{13, modeList, layoutDesktop, legendMaxRows},
		{50, modeList, layoutMobile, 1},
	} {
		m := fleetModel(t, 6, 45, tc.height)
		m.mode, m.layout = tc.mode, tc.layout
		if got := m.legendRows(); got != tc.want {
			t.Errorf("height %d mode %d layout %q: %d legend rows, want %d", tc.height, tc.mode, tc.layout, got, tc.want)
		}
	}
}

// A hidden legend costs the body nothing: lipgloss counts an empty footer as
// one row, and that row used to come off the list.
func TestAHiddenLegendGivesItsRowsToTheBody(t *testing.T) {
	m := fleetModel(t, 87, 45, 12)
	if rows := m.footerRows(); rows != 0 {
		t.Fatalf("footer takes %d rows with the legend hidden", rows)
	}
	if got, want := m.listBodyHeight(), 12-m.listChromeRows()-1; got != want {
		t.Fatalf("body is %d rows, want %d", got, want)
	}
	frame := ansi.Strip(m.frame())
	if lines := strings.Split(frame, "\n"); len(lines) != 12 {
		t.Fatalf("frame is %d rows on a 12-row terminal:\n%s", len(lines), frame)
	}
}

// The comfortable density yields on a short terminal and nowhere else: a
// forty-column rail on a tall terminal still stacks its meta.
func TestComfortableRowsYieldOnAShortTerminal(t *testing.T) {
	tall := fleetModel(t, 6, 40, 50)
	tall.comfortableRows = true
	if !tall.stackedRows() {
		t.Fatal("a tall narrow terminal lost the comfortable density")
	}
	short := fleetModel(t, 6, 200, 27)
	short.comfortableRows = true
	if short.stackedRows() {
		t.Fatal("a short wide terminal kept two-line entries")
	}
	short.layout = layoutDesktop
	if !short.stackedRows() {
		t.Fatal("the desktop override did not restore the density")
	}
}

// The layout setting steps through every mode in order, wraps, persists,
// and reads back on the next model.
func TestSettingsCycleTheLayout(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	for m.settings.field != settingsFieldLayout {
		m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.settings.layout != layoutAuto {
		t.Fatalf("settings opened on %q, want auto", m.settings.layout)
	}
	// Walked against layoutModes rather than against a count written out
	// here, so a mode added to the cycle is covered by this test instead of
	// breaking it.
	for _, want := range append(layoutModes[1:], layoutAuto) {
		m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyRight})
		if m.settings.layout != want {
			t.Fatalf("stepping landed on %q, want %q", m.settings.layout, want)
		}
	}
	for m.settings.layout != layoutMobile {
		m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if chosen, err := m.store.Setting(layoutSetting); err != nil || chosen != layoutMobile {
		t.Fatalf("stored layout = %q, %v; want mobile", chosen, err)
	}
	if m.layout != layoutMobile || !m.tight() {
		t.Fatalf("the saved override did not take: layout %q tight %v", m.layout, m.tight())
	}
	if storedLayout(m.store) != layoutMobile {
		t.Fatal("storedLayout does not read the saved value back")
	}
	if normalizeLayout("phone") != layoutAuto {
		t.Fatal("an unknown stored value is not read as auto")
	}
}
