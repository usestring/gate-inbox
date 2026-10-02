package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/tmux"
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
	if chosen, err := m.store.Setting(m.layoutSettingKey()); err != nil || chosen != layoutMobile {
		t.Fatalf("stored layout = %q, %v; want mobile", chosen, err)
	}
	if m.layout != layoutMobile || !m.tight() {
		t.Fatalf("the saved override did not take: layout %q tight %v", m.layout, m.tight())
	}
	if chosen, err := m.deviceLayout(); err != nil || chosen != layoutMobile {
		t.Fatal("deviceLayout does not read the saved value back")
	}
	if normalizeLayout("phone") != layoutAuto {
		t.Fatal("an unknown stored value is not read as auto")
	}
}

// A phone pinch-zoomed out keeps its screen but gains cells: 54x27 at one
// font size is 90x45 at a smaller one. Auto measures, so it splits there;
// mobile is the operator saying the screen is a phone's, and holds one panel.
func TestMobileLayoutHoldsOnePanelWhenZoomedOut(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		layout        string
		split         bool
	}{
		{54, 27, layoutAuto, false},
		{90, 45, layoutAuto, true},
		{54, 27, layoutMobile, false},
		{90, 45, layoutMobile, false},
		{180, 60, layoutMobile, false},
		{90, 45, layoutDesktop, true},
	} {
		m := fleetModel(t, 6, tc.width, tc.height)
		m.layout = tc.layout
		_, right := m.splitWidths()
		if split := right > 0; split != tc.split {
			t.Errorf("%dx%d %s: split %v, want %v", tc.width, tc.height, tc.layout, split, tc.split)
		}
		if tc.layout == layoutMobile && !m.tight() {
			t.Errorf("%dx%d mobile: chrome not tight", tc.width, tc.height)
		}
	}
}

// The layout follows the device attached, like the theme: mobile chosen on
// the phone stays the phone's, and the laptop keeps the shared default.
func TestDeviceLayoutsPersistAcrossHandoffs(t *testing.T) {
	m := buildModel(t)
	t.Cleanup(func() { applyTheme(themes[0]) })
	attach := func(device string) {
		t.Helper()
		updated, _ := m.Update(visibleMsg{state: "1,1,1", device: device})
		m = updated.(*Model)
	}
	attach("device:phone")
	m.openSettings()
	m.settings.field = settingsFieldLayout
	for m.settings.layout != layoutMobile {
		m.cycleSetting(1)
	}
	m.persistSettings()
	if shared := storedLayout(m.store); shared != layoutAuto {
		t.Fatalf("the phone changed the shared layout to %q", shared)
	}
	attach("device:laptop")
	if m.layout != layoutAuto {
		t.Fatalf("laptop layout = %q, want the shared auto", m.layout)
	}
	if m.settings.layout != layoutAuto {
		t.Fatal("settings kept the phone's layout on the laptop")
	}
	attach("device:phone")
	if m.layout != layoutMobile {
		t.Fatalf("phone layout = %q, want mobile", m.layout)
	}
	if !strings.Contains(m.viewSettings(), "device:phone") {
		t.Fatal("settings hide the layout's device")
	}
}

// The layout is filed under the same device fingerprint as the theme, and
// it survives what a phone does to a board: a detach, a reattach from a new
// SSH connection, and a zoom. Another device does not inherit it.
func TestDeviceLayoutFollowsTheFingerprint(t *testing.T) {
	m := buildModel(t)
	t.Cleanup(func() { applyTheme(themes[0]) })
	attach := func(env ...string) {
		t.Helper()
		updated, _ := m.Update(visibleMsg{state: "1,1,1", device: tmux.DeviceIdentity(env)})
		m = updated.(*Model)
	}
	phone := []string{"SSH_CONNECTION=100.64.0.2 1111 100.64.0.1 22", "TERM_PROGRAM=Termius"}
	attach(phone...)
	m.openSettings()
	m.settings.layout = layoutMobile
	m.persistSettings()
	if m.layoutSettingKey() != "layout:ssh:100.64.0.2/Termius" ||
		strings.TrimPrefix(m.themeSettingKey(), themeSetting) != strings.TrimPrefix(m.layoutSettingKey(), layoutSetting) {
		t.Fatalf("layout key %q does not share the theme's device %q", m.layoutSettingKey(), m.themeSettingKey())
	}

	// Zoomed out: the terminal reports more cells, and nothing else changes.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 45})
	m = updated.(*Model)
	if m.layout != layoutMobile || m.themeDevice != "ssh:100.64.0.2/Termius" {
		t.Fatalf("zoom changed the device or layout: %q %q", m.themeDevice, m.layout)
	}
	if _, right := m.splitWidths(); right != 0 {
		t.Fatal("zoomed-out phone split into two panels")
	}

	// Detached: no client to read, so the device and its layout hold.
	attach()
	if m.layout != layoutMobile {
		t.Fatalf("detach changed the layout to %q", m.layout)
	}

	attach("SSH_CONNECTION=100.64.0.9 1111 100.64.0.1 22", "TERM_PROGRAM=ghostty")
	if m.layout != layoutAuto {
		t.Fatalf("another device inherited %q", m.layout)
	}

	// Back on the phone over a new connection: a new source port, the same
	// fingerprint, the same layout.
	attach("SSH_CONNECTION=100.64.0.2 2222 100.64.0.1 22", "TERM_PROGRAM=Termius")
	if m.layout != layoutMobile {
		t.Fatalf("reconnected phone layout = %q, want mobile", m.layout)
	}
}
