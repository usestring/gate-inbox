package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

// Outside panes are kept as they are unless asked: a real startup leaves an
// idle adopted pane running where it is.
func TestOutsidePanesAreKeptAsIsByDefault(t *testing.T) {
	m := buildModel(t)
	socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
	m.restoreArmed = true
	m.adoptFirstDone = true
	m.applyCmd(t, nil)
	if got := m.outsidePanesMode(); got != paneAdopt {
		t.Fatalf("outside panes default to %q, want keep as-is", got)
	}
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("the default ended the operator's pane")
	}
	if got, _ := m.store.Get("byhand"); got.TmuxPaneID == "" {
		t.Fatal("the default took the row over")
	}
}

// A pane adopted while the board runs gets a toast naming it, and a brings
// it in while the toast is up, whatever row the cursor is on.
func TestTheToastsKeyBringsTheNewPaneIn(t *testing.T) {
	m := buildModel(t)
	m.adoptFirstDone = true
	createSession(t, m, "mine", t.TempDir(), "")
	socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
	m.applyCmd(t, nil)
	m.noteAdopted(adoptedMsg{taken: 1, ids: []string{"byhand"}})
	if !strings.Contains(m.errBar.text, "byhand, started outside the board, is on it as-is · a brings it in · o leaves it out") {
		t.Fatalf("toast = %q", m.errBar.text)
	}
	// The toast outlives the couple of polls an ordinary notice gets.
	for range 4 {
		m.ageError()
	}
	if !m.offerLive(time.Now()) {
		t.Fatal("the toast was gone before its keys could be pressed")
	}
	m.selectSessionRow(t, "mine")
	pressKey(t, m, key("a"))
	if foreignPaneAlive(t, socket, pane) || !m.tmux.Exists("byhand") {
		t.Fatal("a on the toast did not bring the named pane in")
	}
	if !m.tmux.Exists(sessionNamed(t, m, "mine").ID) {
		t.Fatal("a reached the selected board session")
	}
}

// o leaves the pane the toast names off the board, running, and the scan
// never takes it again.
func TestTheToastsOtherKeyLeavesThePaneOut(t *testing.T) {
	m := buildModel(t)
	m.adoptFirstDone = true
	socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
	m.applyCmd(t, nil)
	m.noteAdopted(adoptedMsg{taken: 1, ids: []string{"byhand"}})
	pressKey(t, m, key("o"))
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("o ended the pane")
	}
	if _, err := m.store.Get("byhand"); err == nil {
		t.Fatal("o left the row on the board")
	}
	if len(loadPaneDecisions(m.store).ignoredPaneKeys()) != 1 {
		t.Fatal("o did not keep the scan from taking the pane again")
	}
}

// Without a toast, a acts on the selected row, and only on an adopted one.
func TestBringInActsOnTheSelectedAdoptedRow(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "mine", t.TempDir(), "")
	m.selectSessionRow(t, "mine")
	pressKey(t, m, key("a"))
	if !strings.Contains(m.errBar.text, "already a board session") {
		t.Fatalf("a on a board session said %q", m.errBar.text)
	}
	socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "byhand")
	pressKey(t, m, key("a"))
	if foreignPaneAlive(t, socket, pane) || !m.tmux.Exists("byhand") {
		t.Fatal("a did not bring the selected pane in")
	}
}

// An action that needs a restart refuses an adopted pane by offering the
// way in, rather than only saying no; and v, which restarts a live session
// on its own conversation, is the way in for one.
func TestRestartsOfAnAdoptedPaneOfferToBringItIn(t *testing.T) {
	m := buildModel(t)
	socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
	m.applyCmd(t, nil)
	m.selectSessionRow(t, "byhand")

	m.restartSelected()
	if m.mode == modeConfirmDelete || !strings.Contains(m.errBar.text, "a brings it in") {
		t.Fatalf("restart on an adopted pane: mode %v, %q", m.mode, m.errBar.text)
	}
	if err := m.killSession(sessionNamed(t, m, "byhand")); err == nil || !strings.Contains(err.Error(), "a brings it in") {
		t.Fatalf("killSession refused with %v", err)
	}

	pressKey(t, m, key("v"))
	if m.mode != modeConfirmDelete || !strings.Contains(m.confirm.label, "bring byhand into the board?") {
		t.Fatalf("v on an adopted pane: mode %v, %q", m.mode, m.confirm.label)
	}
	pressKey(t, m, key("y"))
	if foreignPaneAlive(t, socket, pane) || !m.tmux.Exists("byhand") {
		t.Fatal("confirming v did not bring the pane in")
	}
}
