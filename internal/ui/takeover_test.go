package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// adoptPane puts a pane on a foreign server on the board the way the scan
// leaves it: a row carrying the pane's location and a driver resolving the
// id to it. The row starts in the status given.
func adoptForeignPane(t *testing.T, m *Model, id, name, state string) (socket, pane string) {
	t.Helper()
	socket, pane = uiForeignServer(t, "cat")
	if err := m.store.CreateSession(store.Session{
		ID: id, Name: name, Tool: "claude", Cwd: t.TempDir(),
		Status: state, CreatedAt: time.Now(), LastStatusAt: time.Now(),
		TmuxSocket: socket, TmuxPaneID: pane,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := m.tmux.Adopt(id, tmux.Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	return socket, pane
}

// setStatus pins a row's status in the store and on the model, the way a
// poll pass that read the screen would have.
func setStatus(t *testing.T, m *Model, id, state string) {
	t.Helper()
	if err := m.store.UpdateStatus(id, state); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	for i := range m.sessions {
		if m.sessions[i].ID == id {
			m.sessions[i].Status = state
		}
	}
}

// The startup offer is the reopen card: once, on a refresh that finds a pane
// started outside the board, with adopting as-is as the answer it starts on.
// Leaving it as it is keeps the pane and settles it, so the next start does
// not ask about it again.
func TestOutsidePanesAreOfferedOnceAtStartupOnTheReopenCard(t *testing.T) {
	m := buildModel(t)
	adoptForeignPane(t, m, "borrowed", "borrowed", status.Idle)
	m.restoreArmed = true
	m.adoptFirstDone = true

	m.applyCmd(t, nil)
	if m.mode != modeRestorePrompt || len(m.restore.panes) != 1 {
		t.Fatalf("after the first refresh mode = %v panes = %d, want the reopen card", m.mode, len(m.restore.panes))
	}
	out := ansi.Strip(m.frame())
	for _, want := range []string{"Panes started outside the board", "1 agent pane was started outside the board",
		"[adopt as-is]", "misses:", "MCP tools", "flags and model", "never ask"} {
		if !strings.Contains(out, want) {
			t.Fatalf("card missing %q:\n%s", want, out)
		}
	}

	pressKey(t, m, key("n"))
	if m.mode != modeList {
		t.Fatalf("after n mode = %v, want the list", m.mode)
	}
	m.applyCmd(t, nil)
	if m.mode != modeList {
		t.Fatalf("a dismissed card was raised again on the next refresh")
	}
	if got, _ := m.store.Get("borrowed"); got.TmuxPaneID == "" {
		t.Fatal("dismissing the card promoted the row")
	}
	if decided := loadPaneDecisions(m.store); decided["borrowed"] != paneAdopt {
		t.Fatalf("dismissing should settle the pane as kept, ledger = %v", decided)
	}
	// The next start: the answered pane is not asked about again.
	m.restoreAsked = false
	m.applyCmd(t, nil)
	if m.mode != modeList {
		t.Fatalf("an answered pane was offered again on the next start, mode = %v", m.mode)
	}
}

// A model built without Init never asks: a test board, or a headless one.
func TestOutsidePanesAreNotOfferedUnlessArmed(t *testing.T) {
	m := buildModel(t)
	adoptForeignPane(t, m, "borrowed", "borrowed", status.Idle)
	m.applyCmd(t, nil)
	if m.mode != modeList {
		t.Fatalf("an unarmed model raised the offer, mode = %v", m.mode)
	}
}

// Saying yes ends the idle pane in its own window and brings the row back as
// a gi_ session the manager owns, on the same id, name and directory.
func TestTakeoverRestartsAnIdlePaneAsAManagedSession(t *testing.T) {
	m := buildModel(t)
	socket, pane := adoptForeignPane(t, m, "borrowed", "borrowed", status.Idle)
	m.applyCmd(t, nil)

	pressKey(t, m, key("O"))
	if m.mode != modeRestorePrompt || m.restore.paneDefault != paneRelaunch {
		t.Fatalf("O did not open the card on relaunch: mode = %v, err = %q", m.mode, m.errBar.text)
	}
	pressKey(t, m, key("y"))

	if foreignPaneAlive(t, socket, pane) {
		t.Fatal("the adopted pane is still up in its own window")
	}
	got, err := m.store.Get("borrowed")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TmuxPaneID != "" || got.TmuxSocket != "" {
		t.Fatalf("row still adopted after the takeover: socket %q pane %q", got.TmuxSocket, got.TmuxPaneID)
	}
	if got.Name != "borrowed" {
		t.Fatalf("takeover renamed the row to %q", got.Name)
	}
	if _, adopted := m.tmux.AdoptedTarget("borrowed"); adopted {
		t.Fatal("the driver still resolves the id to the foreign pane")
	}
	if !m.tmux.Exists("borrowed") {
		t.Fatal("no managed session came up under the row's id")
	}
	if !strings.Contains(m.errBar.text, "took over 1 adopted session") || !m.errBar.worked() {
		t.Fatalf("status = %q, want the takeover reported as done", m.errBar.text)
	}
	if len(m.takeover.pending) != 0 {
		t.Fatalf("nothing should be owed after the only pane moved, pending = %v", m.takeover.pending)
	}
}

// A busy pane is not restarted on the yes: it is owed, and the refresh that
// first finds it idle is what takes it.
func TestTakeoverWaitsForABusyPaneToGoIdle(t *testing.T) {
	m := buildModel(t)
	socket, pane := adoptForeignPane(t, m, "busy", "busy", status.Working)
	m.applyCmd(t, nil)
	setStatus(t, m, "busy", status.Working)

	pressKey(t, m, key("O"))
	out := ansi.Strip(m.frame())
	if !strings.Contains(out, "[relaunch into the board]") || !strings.Contains(out, "once it is idle") {
		t.Fatalf("the card should say relaunching waits for idle:\n%s", out)
	}
	pressKey(t, m, key("y"))
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("a working pane was ended by the yes")
	}
	if !m.takeover.pending["busy"] {
		t.Fatal("the busy pane is not owed to the background pass")
	}
	if !strings.Contains(m.errBar.text, "1 follows as it goes idle") {
		t.Fatalf("status = %q, want the owed count", m.errBar.text)
	}

	// Still working on the next pass: nothing moves.
	setStatus(t, m, "busy", status.Working)
	if result := m.takeoverPass(); result.taken != 0 || result.owed != 1 {
		t.Fatalf("a working pane was taken: %+v", result)
	}
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("the working pane was ended by a pass")
	}

	setStatus(t, m, "busy", status.Idle)
	if result := m.takeoverPass(); result.taken != 1 || result.owed != 0 {
		t.Fatalf("the idle pane was not taken: %+v", result)
	}
	if foreignPaneAlive(t, socket, pane) {
		t.Fatal("the pane is still up in its own window after going idle")
	}
	if !m.tmux.Exists("busy") {
		t.Fatal("no managed session came up for the pane that went idle")
	}
}

// A tool that resumes by id needs one: without it the relaunch would land on
// the directory's most recent conversation, so the pane is left alone and
// the row says why.
func TestTakeoverLeavesAPaneWhoseConversationCannotBeRead(t *testing.T) {
	m := buildModel(t)
	tool := m.cfg.Tools["claude"]
	tool.ResumeByIDCommand = "cat {id}"
	m.cfg.Tools["claude"] = tool
	socket, pane := adoptForeignPane(t, m, "unread", "unread", status.Idle)
	m.applyCmd(t, nil)

	pressKey(t, m, key("O"))
	pressKey(t, m, key("y"))
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("a pane with no readable conversation was ended")
	}
	got, _ := m.store.Get("unread")
	if got.TmuxPaneID == "" {
		t.Fatal("the row was promoted without a conversation to resume")
	}
	if !strings.Contains(m.errBar.text, "no conversation id") {
		t.Fatalf("status = %q, want the reason the pane was left", m.errBar.text)
	}
	if m.takeover.pending["unread"] {
		t.Fatal("a refused pane must not be retried on every pass")
	}
}

func TestTakeoverKeyWithNothingAdoptedSaysSo(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "own", t.TempDir(), "")
	m.applyCmd(t, nil)
	pressKey(t, m, key("O"))
	if m.mode != modeList || !strings.Contains(m.errBar.text, "no adopted panes") {
		t.Fatalf("mode = %v status = %q", m.mode, m.errBar.text)
	}
}
