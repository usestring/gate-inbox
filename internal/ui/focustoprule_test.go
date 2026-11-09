package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
)

// The ring opens on the frame rule the body already spends: its title and its
// left corner ride the first row, and the mirrored pane starts on the row
// directly under it. Drawing the ring's own top edge one row lower read as a
// single thickened hairline and cost the agent a row of its terminal.
func TestFocusRingOpensOnTheFrameRule(t *testing.T) {
	m := fleetModel(t, 6, 120, 24)
	m.mode = modeFocus
	lines := strings.Split(ansi.Strip(m.frame()), "\n")

	top := lines[0]
	if !strings.Contains(top, "focused:") || !strings.Contains(top, "ctrl+q back") {
		t.Fatalf("the frame's top rule does not name the mode: %q", top)
	}
	if !strings.HasPrefix(top, "╭") || !strings.Contains(top, "╮") {
		t.Errorf("the top rule does not open the ring: %q", top)
	}
	if strings.Contains(lines[1], "╭") {
		t.Errorf("the ring opens a second time on the row below: %q", lines[1])
	}
}

// The body spends no row on a seam of its own, so the pane tmux is pinned to
// is as tall as the body itself.
func TestFocusedPaneKeepsTheWholeBody(t *testing.T) {
	m := fleetModel(t, 6, 120, 24)
	m.mode = modeFocus
	if got, want := m.previewPaneHeight(), m.listBodyHeight(); got != want {
		t.Errorf("the pane is pinned to %d rows of a %d-row body", got, want)
	}
	m.frame()
	if got, want := m.pane.box.y, m.listChromeRows(); got != want {
		t.Errorf("the mirrored pane starts at row %d, want the first body row %d", got, want)
	}
}

// A narrow content column keeps the corner even when it shortens the title.
func TestFocusRuleTailKeepsItsCornerWhenNarrow(t *testing.T) {
	m := fleetModel(t, 6, 120, 24)
	tail := m.focusRuleTail(12, true)
	if got := lipgloss.Width(tail); got != 12 {
		t.Errorf("the tail is %d cells wide, want the 12 it was given", got)
	}
	if !strings.HasSuffix(ansi.Strip(tail), "╮") {
		t.Errorf("the tail does not close the ring: %q", ansi.Strip(tail))
	}
}

func TestFocusRuleKeepsEssentialHintsBesideALongName(t *testing.T) {
	m := fleetModel(t, 6, 65, 12)
	m.mode = modeFocus
	m.chrome = chromeNever
	index, ok := m.selectedIndex()
	if !ok {
		t.Fatal("fixture has no selected session")
	}
	m.rows[index].sess.Name = "fix-file-reservation-normalization-across-all-repository-checkouts"

	for _, tc := range []struct {
		name   string
		scroll int
		triage bool
		hint   string
	}{
		{name: "back", hint: "ctrl+q back"},
		{name: "advance", triage: true, hint: "ctrl+q skip to next"},
		{name: "scrollback", scroll: 12, hint: "12 lines back"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.focusScroll = tc.scroll
			m.triage = tc.triage
			top := ansi.Strip(m.focusRuleTail(65, true))
			if !strings.Contains(top, "fix-file-reservation") || !strings.Contains(top, tc.hint) {
				t.Errorf("border must name the pane and show %q: %q", tc.hint, top)
			}
		})
	}
}

// The same focused border names the input destination as the operator changes
// panes and advances the queue, even with no rail or footer to identify it.
func TestFocusedDestinationFollowsNavigationWithHintsHidden(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"c-working": status.Working, "b-waiting": status.Waiting, "a-finished": status.Finished,
	})
	m.width, m.height = 48, 12
	m.chrome = chromeNever
	m.listSort = listSortName
	m.rebuildRows()

	press := func(key tea.KeyPressMsg) {
		t.Helper()
		updated, _ := m.handleKey(key)
		*m = *updated.(*Model)
	}
	target := func(want string) {
		t.Helper()
		if m.mode != modeFocus || focusedName(t, m) != want {
			t.Fatalf("landed in %q (mode %v), want %s", focusedName(t, m), m.mode, want)
		}
		top := strings.SplitN(frameNow(t, m), "\n", 2)[0]
		if !strings.Contains(top, "focused: "+want) {
			t.Fatalf("border does not name %s: %q", want, top)
		}
		if m.viewFooter() != "" {
			t.Fatal("test is not exercising the border with the footer hidden")
		}
	}
	m.selectSessionRow(t, "a-finished")
	press(tea.KeyPressMsg{Code: tea.KeyTab})
	target("b-waiting")

	// Pane stepping uses list order, independent of the attention jump.
	press(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl})
	target("c-working")
	press(ctrlQ())
	press(runeKey("i"))
	target("b-waiting")
	press(ctrlQ())
	target("a-finished")
	press(ctrlQ())
	if m.mode != modeList {
		t.Fatalf("exhausted triage left input with an agent (mode %v)", m.mode)
	}
	if top := strings.SplitN(frameNow(t, m), "\n", 2)[0]; strings.Contains(top, "focused:") {
		t.Fatalf("exhausted triage still advertises a focused pane: %q", top)
	}
}
