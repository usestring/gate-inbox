package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func typePaneQuery(t *testing.T, m *Model, query string) {
	t.Helper()
	for _, char := range query {
		m.handleKey(key(string(char)))
	}
}

func TestPaneJumpMatchesMetadataAcrossFoldedGroups(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("backend/deep", ""); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	createSession(t, m, "unique-worker", t.TempDir(), "backend/deep")
	m.collapsed["backend"], m.collapsed["backend/deep"] = true, true
	m.search = "something-else"
	m.statusFilter = statusFilterAttention
	m.rebuildRows()
	m.handleKey(jumpKey(t, "alt+p"))
	if m.mode != modePaneJump {
		t.Fatalf("mode = %v", m.mode)
	}
	typePaneQuery(t, m, "uniqwrk")
	matches := m.paneJumpMatches()
	if len(matches) != 1 || matches[0].Name != "unique-worker" {
		t.Fatalf("matches = %v", matches)
	}
	id := matches[0].ID
	for _, query := range []string{"backend", matches[0].Tool, matches[0].Status} {
		m.paneJump.input.SetValue(query)
		if hits := m.paneJumpMatches(); len(hits) != 1 || hits[0].ID != id {
			t.Fatalf("query %q: %v", query, hits)
		}
	}
	m.handleKey(key("enter"))
	if m.mode != modeFocus || m.focusedID != id {
		t.Fatalf("mode %v, focus %s, error %q", m.mode, m.focusedID, m.errBar.text)
	}
	if m.collapsed["backend"] || m.collapsed["backend/deep"] || m.search != "" || m.statusFilter != statusFilterAll {
		t.Fatal("jump did not reveal the selected pane")
	}
}

func TestPaneJumpCancelPreservesListAndFocusedView(t *testing.T) {
	for _, focused := range []bool{false, true} {
		t.Run(map[bool]string{false: "list", true: "focused"}[focused], func(t *testing.T) {
			m := buildModel(t)
			createSession(t, m, "alpha", t.TempDir(), "")
			createSession(t, m, "beta", t.TempDir(), "")
			m.selectSessionRow(t, "alpha")
			if focused {
				focusRow(t, m, "alpha")
				m.focusScroll = 7
			}
			m.search = "alpha"
			m.rebuildRows()
			m.collapsed["other"] = true
			mode, cursor, focus := m.mode, m.cursor, m.focusedID
			m.errBar.text = "original notice"
			m.handleKey(jumpKey(t, "alt+p"))
			typePaneQuery(t, m, "beta")
			m.handleKey(key("esc"))
			if m.mode != mode || m.cursor != cursor || m.focusedID != focus || m.search != "alpha" || !m.collapsed["other"] || m.errBar.text != "original notice" {
				t.Fatalf("cancel changed current view: mode %v, cursor %d, focus %s, search %q, notice %q", m.mode, m.cursor, m.focusedID, m.search, m.errBar.text)
			}
			if focused && m.focusScroll != 7 {
				t.Fatal("cancel reset focused scroll")
			}
		})
	}
}

func TestPaneJumpFromFocusSelectsNewPaneAndKeepsPreviousPane(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	createSession(t, m, "beta", t.TempDir(), "")
	focusRow(t, m, "alpha")
	old := m.focusedID
	m.handleKey(jumpKey(t, "alt+p"))
	typePaneQuery(t, m, "beta")
	m.handleKey(key("enter"))
	sess, ok := m.selected()
	if !ok || sess.Name != "beta" || m.mode != modeFocus || m.prevFocusID != old {
		t.Fatalf("jump from focus = %v, mode %v, previous %s", sess, m.mode, m.prevFocusID)
	}
}

func TestPaneJumpRejectsEmptyAndClosedResultsWithoutRetargeting(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	createSession(t, m, "beta", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	m.handleKey(jumpKey(t, "alt+p"))
	typePaneQuery(t, m, "unmatched-zzzz")
	m.handleKey(key("enter"))
	if m.mode != modePaneJump || m.errBar.text != "no live pane selected" {
		t.Fatal("empty search focused a pane")
	}
	m.handleKey(key("esc"))
	m.handleKey(jumpKey(t, "alt+p"))
	typePaneQuery(t, m, "beta")
	target := m.paneJump.selectedID
	if err := m.tmux.Kill(target); err != nil {
		t.Fatal(err)
	}
	m.handleKey(key("enter"))
	if m.mode != modePaneJump || m.errBar.text != "pane closed: choose another" {
		t.Fatalf("stale selection = %v, %q", m.mode, m.errBar.text)
	}
	m.handleKey(key("esc"))
	if sess, _ := m.selected(); sess.Name != "alpha" {
		t.Fatalf("closed result moved cursor to %v", sess)
	}
}

func TestPaneJumpSelectionSurvivesRefreshReordering(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	createSession(t, m, "beta", t.TempDir(), "")
	m.handleKey(jumpKey(t, "alt+p"))
	typePaneQuery(t, m, "beta")
	id := m.paneJump.selectedID
	slices.Reverse(m.sessions)
	m.handleKey(key("enter"))
	if m.focusedID != id {
		t.Fatalf("reordered result focused %s, want %s", m.focusedID, id)
	}
	m.leaveFocusForFixture(t)
	m.handleKey(jumpKey(t, "alt+p"))
	m.sessions = slices.DeleteFunc(m.sessions, func(sess store.Session) bool { return sess.ID == id })
	m.handleKey(key("enter"))
	if m.mode != modePaneJump || m.focusedID != id {
		t.Fatal("removed result silently focused a different pane")
	}
}

func TestPaneJumpExcludesDeadAndArchivedSessions(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	createSession(t, m, "beta", t.TempDir(), "")
	m.sessions[0].Archived = true
	m.sessions[1].Status = status.Dead
	m.handleKey(jumpKey(t, "alt+p"))
	if matches := m.paneJumpMatches(); len(matches) != 0 {
		t.Fatalf("matches = %v", matches)
	}
	if view := ansi.Strip(m.viewPaneJump()); !strings.Contains(view, "no live panes match") {
		t.Fatalf("empty view = %s", view)
	}
}

func TestPaneJumpKeyboardNavigationAndRebinding(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	createSession(t, m, "beta", t.TempDir(), "")
	m.keys, _ = m.km().Rebind(keymap.ContextList, keymap.PaneJump, []string{"P"})
	m.handleKey(key("P"))
	if m.mode != modePaneJump {
		t.Fatal("rebound key did not open picker")
	}
	first := m.paneJump.selectedID
	m.handleKey(key("down"))
	if m.paneJump.selectedID == first {
		t.Fatal("down did not select next pane")
	}
	m.handleKey(key("up"))
	if m.paneJump.selectedID != first {
		t.Fatal("up did not select previous pane")
	}
	m.handleKey(key("up"))
	if m.paneJump.selectedID == first {
		t.Fatal("up did not wrap")
	}
}

func TestPaneJumpRowsFitNarrowAndShortScreens(t *testing.T) {
	m := buildModel(t)
	for _, size := range [][2]int{{40, 20}, {80, 24}, {160, 50}} {
		m.width, m.height = size[0], size[1]
		m.sessions = nil
		m.paneJump.live = map[string]bool{}
		for i := 0; i < 30; i++ {
			id := strings.Repeat("x", i+1)
			m.sessions = append(m.sessions, store.Session{ID: id, Name: "a long session name with wide characters 日本語", Tool: "claude", Group: "backend/deep", Status: status.Idle})
			m.paneJump.live[id] = true
		}
		m.openPaneJump()
		m.paneJump.live = map[string]bool{}
		for _, sess := range m.sessions {
			m.paneJump.live[sess.ID] = true
		}
		view := m.viewPaneJump()
		if rows := strings.Count(view, "\n") + 1; rows > m.height {
			t.Fatalf("height %d: %d rows", m.height, rows)
		}
		for _, line := range strings.Split(view, "\n") {
			if width := ansi.StringWidth(line); width > m.width {
				t.Fatalf("width %d: line %d", m.width, width)
			}
		}
	}
}

func TestPaneJumpConsumesMouseWithoutChangingPreview(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	focusRow(t, m, "alpha")
	m.focusScroll = 3
	m.handleKey(jumpKey(t, "alt+p"))
	cursor := m.cursor
	m.handleMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 70, Y: 10})
	if m.cursor != cursor || m.focusScroll != 3 {
		t.Fatal("picker mouse changed underlying view")
	}
}

func TestPaneJumpCancelDoesNotFocusReplacementAfterOriginalDisappears(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	createSession(t, m, "beta", t.TempDir(), "")
	focusRow(t, m, "alpha")
	id := m.focusedID
	m.handleKey(jumpKey(t, "alt+p"))
	m.sessions = slices.DeleteFunc(m.sessions, func(sess store.Session) bool { return sess.ID == id })
	m.rebuildRows()
	m.handleKey(key("esc"))
	if m.mode != modeList || m.focusedID != id {
		t.Fatal("cancel focused another pane after the original disappeared")
	}
}

func TestPaneJumpRevealsNestedChildFolds(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	createSession(t, m, "beta", t.TempDir(), "")
	createSession(t, m, "nested", t.TempDir(), "")
	var parentID, childID string
	for _, sess := range m.sessions {
		if sess.Name == "alpha" {
			parentID = sess.ID
		}
		if sess.Name == "beta" {
			childID = sess.ID
		}
	}
	for i := range m.sessions {
		switch m.sessions[i].Name {
		case "beta":
			m.sessions[i].ParentID = parentID
		case "nested":
			m.sessions[i].ParentID = childID
		}
	}
	m.setChildrenFolded(parentID, true)
	m.setChildrenFolded(childID, true)
	m.rebuildRows()
	m.handleKey(jumpKey(t, "alt+p"))
	typePaneQuery(t, m, "nested")
	m.handleKey(key("enter"))
	sess, _ := m.selected()
	if m.mode != modeFocus || sess.Name != "nested" || !m.childrenShown(parentID) || !m.childrenShown(childID) {
		t.Fatalf("nested target = %v, mode %v, error %q", sess, m.mode, m.errBar.text)
	}
}

func TestPaneJumpLeavesScopedTriageOnlyWhenSelectionIsOutsideIt(t *testing.T) {
	m := buildModel(t)
	for _, group := range []string{"alpha", "beta"} {
		if err := m.store.CreateGroup(group, ""); err != nil {
			t.Fatal(err)
		}
	}
	loadStoredRows(t, m)
	createSession(t, m, "alpha-pane", t.TempDir(), "alpha")
	createSession(t, m, "beta-pane", t.TempDir(), "beta")
	m.triage = true
	m.triageScope = "alpha"
	m.rebuildRows()
	m.handleKey(jumpKey(t, "alt+p"))
	typePaneQuery(t, m, "beta-pane")
	m.handleKey(key("enter"))
	sess, _ := m.selected()
	if m.mode != modeFocus || sess.Name != "beta-pane" || m.triage || m.triageScope != "" {
		t.Fatalf("scoped jump = %v, mode %v", sess, m.mode)
	}
}
