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

// The startup offer: once, on a refresh that finds adopted panes, naming the
// idle ones it would restart now.
func TestTakeoverIsOfferedOnceAtStartup(t *testing.T) {
	m := buildModel(t)
	adoptForeignPane(t, m, "borrowed", "borrowed", status.Idle)
	m.takeover.offer = true

	m.applyCmd(t, nil)
	if m.mode != modeConfirmDelete || m.confirm.action != actionTakeover {
		t.Fatalf("after the first refresh mode = %v action = %q, want the takeover dialog", m.mode, m.confirm.action)
	}
	out := ansi.Strip(m.frame())
	for _, want := range []string{"Take over adopted panes", "restart 1 idle adopted session", "take over", "cancel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dialog missing %q:\n%s", want, out)
		}
	}

	pressKey(t, m, key("n"))
	if m.mode != modeList {
		t.Fatalf("after n mode = %v, want the list", m.mode)
	}
	m.applyCmd(t, nil)
	if m.mode != modeList {
		t.Fatalf("a declined offer was raised again on the next refresh")
	}
	if got, _ := m.store.Get("borrowed"); got.TmuxPaneID == "" {
		t.Fatal("declining the offer promoted the row")
	}
}

// A model built without Init never asks: a test board, or a headless one.
func TestTakeoverIsNotOfferedUnlessArmed(t *testing.T) {
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
	if m.mode != modeConfirmDelete || m.confirm.action != actionTakeover {
		t.Fatalf("O did not open the dialog: mode = %v, err = %q", m.mode, m.errBar.text)
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
	if !strings.Contains(out, "take over 1 adopted pane as it goes idle?") {
		t.Fatalf("a dialog over only busy panes should say it waits:\n%s", out)
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

// The dialog over a mixed board says both halves: what goes now and what
// follows.
func TestTakeoverLabelNamesNowAndLater(t *testing.T) {
	label := takeoverLabel([]store.Session{
		{ID: "a", Status: status.Idle}, {ID: "b", Status: status.Idle}, {ID: "c", Status: status.Working},
	})
	for _, want := range []string{"restart 2 idle adopted sessions as managed ones now?", "1 busy one follows on its own as it goes idle."} {
		if !strings.Contains(label, want) {
			t.Fatalf("label %q missing %q", label, want)
		}
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

// End-to-end: a real foreign pane discovered by adoptScan lands on the board
// as an adopted row, and pressing O then takes it over as a managed session.
// This is the regression guard that ensures scan → identify → board → O wiring
// stays intact.
func TestAdoptionE2E(t *testing.T) {
	m := buildModel(t)

	// Spin up a real foreign tmux pane running a tool that matches both
	// adoption signals: the binary name and the prompt marker.
	socket, pane := uiForeignServer(t, "claude")
	if socket == "" || pane == "" {
		t.Skip("uiForeignServer could not create a foreign pane")
	}

	// First scan without the pane in the store: nothing to know about yet.
	cmd := m.adoptScan()
	m.applyCmd(t, cmd)

	// The scan runs and the pane should now be on the board as adopted.
	// Find it in m.sessions.
	var adopted *store.Session
	for i := range m.sessions {
		if m.sessions[i].TmuxSocket == socket && m.sessions[i].TmuxPaneID == pane {
			adopted = &m.sessions[i]
			break
		}
	}
	if adopted == nil {
		t.Fatalf("adoptScan did not land the foreign pane on the board. Session list: %v", m.sessions)
	}
	adoptedID := adopted.ID
	if adopted.TmuxSocket != socket || adopted.TmuxPaneID != pane {
		t.Fatalf("adopted row: socket %q pane %q, want %q %q", adopted.TmuxSocket, adopted.TmuxPaneID, socket, pane)
	}

	// Set the pane to idle so it is immediately takeover-ready.
	setStatus(t, m, adoptedID, status.Idle)

	// Press O: the dialog should open offering to take over the one idle pane.
	pressKey(t, m, key("O"))
	if m.mode != modeConfirmDelete || m.confirm.action != actionTakeover {
		t.Fatalf("O did not open the takeover dialog: mode %v action %q err %q", m.mode, m.confirm.action, m.errBar.text)
	}

	// Confirm with y: the pane should be ended and relaunched as managed.
	pressKey(t, m, key("y"))

	// Verify the foreign pane is gone.
	if foreignPaneAlive(t, socket, pane) {
		t.Fatal("the adopted pane is still up after takeover confirmation")
	}

	// Verify the row is no longer marked adopted.
	got, err := m.store.Get(adoptedID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TmuxSocket != "" || got.TmuxPaneID != "" {
		t.Fatalf("row still adopted: socket %q pane %q", got.TmuxSocket, got.TmuxPaneID)
	}

	// Verify a managed session came up on that id.
	if !m.tmux.Exists(adoptedID) {
		t.Fatal("no managed gi_ session was created for the adopted pane")
	}

	// The whole chain worked: scan found it, board showed it, O took it over.
}
