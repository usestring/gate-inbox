package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestNewBuildRestartsAnIdleBoard(t *testing.T) {
	m := &Model{mode: modeList, lastInteractionAt: time.Now().Add(-time.Minute)}
	_, cmd := m.restartWhenIdle()
	if !m.RestartRequested() {
		t.Fatal("an idle board on its list did not restart for a new build")
	}
	if cmd == nil {
		t.Fatal("restart returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("restart did not quit the program")
	}
}

// A restart redraws the screen and drops whatever was being typed, so it
// waits while the operator is using the board.
func TestNewBuildWaitsForTheOperator(t *testing.T) {
	for name, m := range map[string]*Model{
		"just typed": {mode: modeList, lastInteractionAt: time.Now()},
		"searching":  {mode: modeList, searching: true, lastInteractionAt: time.Now().Add(-time.Minute)},
		"in a form":  {mode: modeForm, lastInteractionAt: time.Now().Add(-time.Minute)},
	} {
		_, cmd := m.restartWhenIdle()
		if m.RestartRequested() {
			t.Fatalf("%s: board restarted under the operator", name)
		}
		if cmd == nil {
			t.Fatalf("%s: a deferred restart scheduled no retry", name)
		}
	}
}

// Keys typed inside an attached session never reach the board, so a long
// attach must not read as the operator having walked away.
func TestReturningFromASessionCountsAsActivity(t *testing.T) {
	m := buildModel(t)
	m.lastInteractionAt = time.Now().Add(-time.Hour)
	updated, _ := m.Update(attachDoneMsg{})
	m = updated.(*Model)
	if m.upgradeSafe(time.Now()) {
		t.Fatal("a board the operator just came back to counted as idle")
	}
}
