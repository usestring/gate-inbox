package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The whole feature from the list: one press puts the rail away and the
// split goes with it, a second brings both back, and the choice is in the
// store either way, so a rail put away stays away across a restart.
func TestRailToggleKeyHidesAndRestoresTheSplit(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "railtoggle", t.TempDir(), "")
	m.selectSessionRow(t, "railtoggle")
	m.width, m.height = 120, 40

	updated, _ := m.handleKey(key(`\`))
	*m = *updated.(*Model)
	if m.layout != layoutBoard {
		t.Fatalf("layout %q after the first press, want %q (err %q)", m.layout, layoutBoard, m.errBar.text)
	}
	if _, right := m.splitWidths(); right != 0 {
		t.Errorf("the preview column kept %d cells with the rail toggled away", right)
	}
	if got := storedLayout(m.store); got != layoutBoard {
		t.Errorf("stored layout %q, want %q", got, layoutBoard)
	}

	updated, _ = m.handleKey(key(`\`))
	*m = *updated.(*Model)
	if m.layout != layoutAuto {
		t.Fatalf("layout %q after the second press, want %q", m.layout, layoutAuto)
	}
	if _, right := m.splitWidths(); right == 0 {
		t.Errorf("the preview column did not come back on a %d-wide terminal", m.width)
	}
	if got := storedLayout(m.store); got != layoutAuto {
		t.Errorf("stored layout %q, want %q", got, layoutAuto)
	}
}

// Bringing the rail back gives the operator the mode they chose, not the
// width tiers: somebody on "mobile" asked for the tight layout, and taking
// the columns for a moment is not them changing their mind.
func TestRailToggleRestoresTheModeItHid(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "railmobile", t.TempDir(), "")
	m.selectSessionRow(t, "railmobile")
	m.layout = layoutMobile

	for i := 0; i < 2; i++ {
		updated, _ := m.handleKey(key(`\`))
		*m = *updated.(*Model)
	}
	if m.layout != layoutMobile {
		t.Errorf("layout %q after hiding and showing the rail again, want %q", m.layout, layoutMobile)
	}
}

// Focused, the rail is still the manager's own columns: alt+\ gives them to
// the pane without leaving the session, and the plain backslash stays the
// agent's character.
func TestFocusAltBackslashTogglesTheRail(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "railfocus", t.TempDir(), "")
	m.selectSessionRow(t, "railfocus")
	m.width, m.height = 120, 40

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: '\\', Mod: tea.ModAlt})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf(`alt+\ left the pane, mode = %v`, m.mode)
	}
	if m.layout != layoutBoard {
		t.Fatalf(`layout %q after alt+\ in focus, want %q (err %q)`, m.layout, layoutBoard, m.errBar.text)
	}
	if _, right := m.splitWidths(); right != 0 {
		t.Errorf("the rail kept %d cells in a focused session that asked for the width", right)
	}
}

// The badge is the way back out. With the rail away in a focused session
// there is no list to print a key on, so the board carries it.
func TestRailToggleNamesTheKeyBack(t *testing.T) {
	m := fleetModel(t, 6, 120, 40)
	m.layout = layoutBoard
	frame := ansi.Strip(m.frame())
	if !strings.Contains(frame, "WIDE") {
		t.Fatalf("no WIDE badge on a board that hid its preview:\n%s", frame)
	}
}
