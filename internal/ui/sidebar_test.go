package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
)

// The settings screen's stored choice outranks the config file; with none
// stored, the file decides, and anything unreadable is the right side.
func TestStoredSidebar(t *testing.T) {
	for _, tc := range []struct {
		name       string
		stored     string
		configured string
		want       string
	}{
		{name: "nothing anywhere", want: config.SidebarRight},
		{name: "config left", configured: "left", want: config.SidebarLeft},
		{name: "config right", configured: "right", want: config.SidebarRight},
		{name: "stored left beats config right", stored: "left", configured: "right", want: config.SidebarLeft},
		{name: "stored right beats config left", stored: "right", configured: "left", want: config.SidebarRight},
		{name: "unknown stored value", stored: "top", configured: "left", want: config.SidebarRight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := memSettings{}
			if tc.stored != "" {
				st[sidebarSetting] = tc.stored
			}
			if got := storedSidebar(st, tc.configured); got != tc.want {
				t.Fatalf("storedSidebar = %q, want %q", got, tc.want)
			}
		})
	}
}

// A choice matching the config file stores nothing, so the file stays in
// charge; only a side that differs is written.
func TestSidebarOverride(t *testing.T) {
	for _, tc := range []struct{ chosen, configured, want string }{
		{"right", "", ""},
		{"left", "left", ""},
		{"left", "right", config.SidebarLeft},
		{"right", "left", config.SidebarRight},
	} {
		if got := sidebarOverride(tc.chosen, tc.configured); got != tc.want {
			t.Fatalf("sidebarOverride(%q, %q) = %q, want %q", tc.chosen, tc.configured, got, tc.want)
		}
	}
}

// With the rail on the left the seam sits past the rail's columns, and the
// ratio still means the rail's share, so a stored split keeps its size when
// the side changes.
func TestSplitGeometryRailLeft(t *testing.T) {
	m := &Model{width: 100, split: splitState{ratio: 0.3}, sidebar: config.SidebarLeft}
	if got := m.dividerX(); got != 30 {
		t.Fatalf("dividerX = %d, want 30 (the rail's 30 columns to its left)", got)
	}
	m.setSplitFromX(40)
	if m.split.ratio != 0.4 {
		t.Fatalf("ratio = %v, want 0.4", m.split.ratio)
	}
	m.split.resizeMode = true
	m.nudgeSplit(1)
	if rail, _ := m.splitWidths(); rail != 41 {
		t.Fatalf("right arrow should widen a left rail: rail = %d, want 41", rail)
	}
	m.sidebar = config.SidebarRight
	if rail, _ := m.splitWidths(); rail != 41 {
		t.Fatalf("switching sides changed the rail's width to %d", rail)
	}
	if got := m.dividerX(); got != 100-41-1 {
		t.Fatalf("right rail dividerX = %d, want %d", got, 100-41-1)
	}
}

// The exit arrow out of focus is the one pointing at the rail.
func TestRailOnRightFollowsSidebar(t *testing.T) {
	m := &Model{}
	if !m.railOnRight() {
		t.Fatal("an unset sidebar should keep the rail on the right")
	}
	m.sidebar = config.SidebarLeft
	if m.railOnRight() {
		t.Fatal("a left sidebar should report the rail off the right")
	}
}

// The left-rail frame fills the terminal exactly, draws the sessions left
// of the seam and the pane right of it, and records the pane's origin where
// it painted it.
func TestFrameRailLeft(t *testing.T) {
	for _, width := range []int{80, 120, 200} {
		for _, height := range []int{24, 50} {
			m := shotModel()
			m.sidebar = config.SidebarLeft
			m.width, m.height = width, height
			raw := strings.Split(m.viewListFrame(), "\n")
			if len(raw) != height {
				t.Fatalf("%dx%d: frame paints %d rows", width, height, len(raw))
			}
			for i, line := range raw {
				if got := ansi.StringWidth(line); got != width {
					t.Fatalf("%dx%d: line %d is %d wide: %q", width, height, i, got, ansi.Strip(line))
				}
			}
			seam := m.dividerX()
			rail, _ := m.splitWidths()
			if seam != rail || m.pane.columnX != seam+2 {
				t.Fatalf("%dx%d: pane starts at %d, seam %d, rail %d", width, height, m.pane.columnX, seam, rail)
			}
			listed, previewed := false, false
			for _, row := range strings.Split(ansi.Strip(strings.Join(raw, "\n")), "\n") {
				runes := []rune(row)
				if strings.Contains(string(runes[:seam]), "add-rate-limiting") {
					listed = true
				}
				if strings.Contains(string(runes[:seam+1]), "token bucket") {
					t.Fatalf("%dx%d: preview drawn left of the seam: %q", width, height, row)
				}
				if strings.Contains(string(runes[seam+1:]), "token bucket") {
					previewed = true
				}
			}
			if !listed || !previewed {
				t.Fatalf("%dx%d: session listed left of the seam %v, preview right of it %v", width, height, listed, previewed)
			}
		}
	}
}

// Focused with the rail on the left, the ring's left upright is the bleed
// column just past the seam and its right upright is the frame's last
// column, and tmux keeps the width the right-rail frame gives it.
func TestFocusedPaneRingRailLeft(t *testing.T) {
	for _, width := range []int{80, 120, 200} {
		for _, height := range []int{12, 24, 50} {
			m := shotModel()
			m.width, m.height, m.mode = width, height, modeFocus
			rightWidth := m.previewPaneWidth()
			m.sidebar = config.SidebarLeft
			if m.previewPaneWidth() != rightWidth {
				t.Fatal("switching sides changed the tmux width")
			}
			m.preview = strings.Repeat("x", m.previewPaneWidth()-1) + "Z"
			rows := strings.Split(ansi.Strip(m.frame()), "\n")
			if m.pane.box.width != m.previewPaneWidth() {
				t.Fatalf("painted width %d differs from tmux width %d", m.pane.box.width, m.previewPaneWidth())
			}
			left := m.pane.box.x - 1
			right := m.pane.box.x + m.pane.box.width
			if left != m.dividerX()+1 {
				t.Fatalf("%dx%d: ring's left upright at %d, want the bleed column %d", width, height, left, m.dividerX()+1)
			}
			if right != width-1 {
				t.Fatalf("%dx%d: ring's right upright at %d, want the last column %d", width, height, right, width-1)
			}
			if got := []rune(rows[m.pane.box.y])[right-1]; got != 'Z' {
				t.Fatalf("%dx%d: border overwrote the pane's last column, got %q", width, height, got)
			}
			top, bottom := m.pane.box.y-1, m.listChromeRows()+m.listBodyHeight()
			for y := top; y <= bottom; y++ {
				wantLeft, wantRight := '│', '│'
				if y == top {
					wantLeft, wantRight = '╭', '╮'
				}
				if y == bottom {
					wantLeft, wantRight = '╰', '╯'
				}
				runes := []rune(rows[y])
				if runes[left] != wantLeft || runes[right] != wantRight {
					t.Fatalf("%dx%d row %d: uprights %q %q, want %q %q", width, height, y, runes[left], runes[right], wantLeft, wantRight)
				}
			}
			for y, row := range rows {
				if got := ansi.StringWidth(row); got > width {
					t.Fatalf("row %d width %d exceeds %d", y, got, width)
				}
			}
		}
	}
}

// The settings screen lists the sidebar, moves the rail as it is stepped,
// and stores only a side that differs from the config file.
func TestSettingsSidebarAppliesLiveAndPersists(t *testing.T) {
	m := buildModel(t)
	if m.sidebar != config.SidebarRight {
		t.Fatalf("a fresh board should draw the rail on the right, got %q", m.sidebar)
	}
	m.openSettings()
	for i := 0; i < settingsFieldSidebar; i++ {
		m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.settings.field != settingsFieldSidebar {
		t.Fatalf("stepping down should reach the sidebar field, got %d", m.settings.field)
	}
	if !strings.Contains(ansi.Strip(m.viewSettings()), "sidebar") {
		t.Fatal("settings should list the sidebar")
	}
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.railOnLeft() {
		t.Fatal("stepping the sidebar should move the rail before the panel closes")
	}
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got, _ := m.store.Setting(sidebarSetting); got != config.SidebarLeft {
		t.Fatalf("stored sidebar = %q, want left", got)
	}
	if got := storedSidebar(m.store, m.cfg.Board.Sidebar); got != config.SidebarLeft {
		t.Fatalf("a restart would draw the rail %q, want left", got)
	}

	m.openSettings()
	m.settings.field = settingsFieldSidebar
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.handleSettingsKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.railOnLeft() {
		t.Fatal("stepping back should put the rail on the right")
	}
	if got, _ := m.store.Setting(sidebarSetting); got != "" {
		t.Fatalf("choosing the config's side should clear the override, stored %q", got)
	}
}

// With the rail on the left the exit arrow mirrors: Left at the head of the
// prompt steps back to the list, and Right, even at the prompt's end, is
// the agent's.
func TestFocusLeftUnfocusesAtPromptHeadRailLeft(t *testing.T) {
	m := buildModel(t)
	m.sidebar = config.SidebarLeft
	createSession(t, m, "leftrail", t.TempDir(), "")
	m.selectSessionRow(t, "leftrail")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	sess := m.rows[m.cursor].sess
	m.rows[m.cursor].sess.Tool = "claude-hooked"
	m.pane.forID = sess.ID
	m.preview = "❯ hi\n"

	m.pane.cursor = paneCursor{x: 4, y: 0, ok: true}
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("right at the prompt end left focus with the rail on the left, mode = %v", m.mode)
	}

	m.pane.cursor = paneCursor{x: 2, y: 0, ok: true}
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	*m = *updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("left at the prompt head did not unfocus with the rail on the left, mode = %v", m.mode)
	}
}
