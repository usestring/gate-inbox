package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestPanePickerMatchesVisibleSessionsAndLeavesFiltersAlone(t *testing.T) {
	m := searchModel()
	m.sessions = append(m.sessions, store.Session{ID: "4", Name: "old-pane", Status: status.Dead})
	m.search = "web"
	m.rebuildRows()
	m.openPanePicker()
	if matches := m.panePickMatches(); len(matches) != 1 || matches[0].sess.Name != "web-ui" {
		t.Fatalf("matches = %+v, want the one listed live pane", matches)
	}
	m.handlePanePickerKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.mode != modeList || m.search != "web" {
		t.Fatalf("cancel changed the list: mode=%v search=%q", m.mode, m.search)
	}
}

func TestPanePickerSwitchesFocusedPaneAndCancelReturnsToIt(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "first", t.TempDir(), rootGroup)
	createSession(t, m, "second", t.TempDir(), rootGroup)
	m.selectSessionRow(t, "first")
	m.focusSelected()
	if m.mode != modeFocus {
		t.Fatalf("could not focus first pane: %s", m.errBar.text)
	}
	pressKey(t, m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if m.mode != modePanePicker {
		t.Fatalf("ctrl+g opened mode %v, want pane picker", m.mode)
	}
	pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.mode != modeFocus || focusedName(t, m) != "first" {
		t.Fatalf("cancel left mode %v on %q", m.mode, focusedName(t, m))
	}
	pressKey(t, m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m.panePicker.input.SetValue("second")
	pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeFocus || focusedName(t, m) != "second" {
		t.Fatalf("jump left mode %v on %q: %s", m.mode, focusedName(t, m), m.errBar.text)
	}
	if m.prevFocusID == "" {
		t.Fatal("jump did not keep the previous pane for alt+l")
	}
}

func TestPanePickerRevealsPaneInsideFoldedGroup(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.collapsed["beta"] = true
	m.rebuildRows()
	m.openPanePicker()
	m.panePicker.input.SetValue("new-block")
	if matches := m.panePickMatches(); len(matches) != 1 || matches[0].sess.Name != "new-block" {
		t.Fatalf("folded pane matches = %+v", matches)
	}
	m.handlePanePickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.collapsed["beta"] || m.rows[m.cursor].sess.Name != "new-block" {
		t.Fatalf("jump did not reveal new-block in beta: cursor=%d, collapsed=%v", m.cursor, m.collapsed["beta"])
	}
}
