package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
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

// A real startup takes an adopted pane over without asking: no card, the idle
// pane ended in its own window and back as a board session, and the line on
// the status bar says so.
func TestAnAdoptedPaneIsTakenOverAtStartupWithoutAsking(t *testing.T) {
	m := buildModel(t)
	socket, pane := adoptForeignPane(t, m, "borrowed", "borrowed", status.Idle)
	m.restoreArmed = true
	m.adoptFirstDone = true

	m.applyCmd(t, nil)
	if m.mode != modeList {
		t.Fatalf("mode = %v, want the list: panes are not asked about", m.mode)
	}
	if foreignPaneAlive(t, socket, pane) {
		t.Fatal("the idle adopted pane is still up in its own window")
	}
	if got, _ := m.store.Get("borrowed"); got.TmuxPaneID != "" || !m.tmux.Exists("borrowed") {
		t.Fatalf("the pane is not a board session now: %+v", got)
	}
	if !strings.Contains(m.errBar.text, "took over 1 adopted session") {
		t.Fatalf("status = %q, want the takeover reported", m.errBar.text)
	}
}

func TestAutoTakeoverWaitsForAnAttachedTmuxClient(t *testing.T) {
	m := buildModel(t)
	socket, pane := adoptForeignPane(t, m, "borrowed", "borrowed", status.Idle)
	terminal := tmuxtest.Socket(t, "takeoverterminal")
	tmuxOnSocket(terminal, "kill-server").Run()
	t.Cleanup(func() { tmuxOnSocket(terminal, "kill-server").Run() })
	command := "tmux -L " + socket + " attach-session -t user"
	if out, err := tmuxOnSocket(terminal, "new-session", "-d", "-s", "terminal", "-x", "80", "-y", "24", command).CombinedOutput(); err != nil {
		t.Fatalf("attach foreign client: %v: %s", err, out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		attached, err := m.tmux.PaneState("borrowed", "#{session_attached}")
		if err == nil && strings.TrimSpace(attached) == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("foreign client did not attach: %q, %v", attached, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.applyCmd(t, nil)
	setStatus(t, m, "borrowed", status.Idle)
	m.restoreArmed = true
	m.adoptFirstDone = true
	if result := m.takeoverPass(); result.taken != 0 || result.owed != 1 {
		t.Fatalf("attached pane was taken: %+v", result)
	}
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("attached pane was ended")
	}
	if err := tmuxOnSocket(terminal, "kill-server").Run(); err != nil {
		t.Fatalf("detach foreign client: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		result := m.takeoverPass()
		if result.taken == 1 && result.owed == 0 {
			break
		}
		if time.Now().After(deadline) || len(result.failed) > 0 {
			t.Fatalf("detached idle pane was not taken: %+v", result)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A model built without Init takes nothing on its own: a test board, or a
// headless one.
func TestAdoptedPanesAreNotTakenOverUnlessArmed(t *testing.T) {
	m := buildModel(t)
	socket, pane := adoptForeignPane(t, m, "borrowed", "borrowed", status.Idle)
	m.applyCmd(t, nil)
	if m.mode != modeList || !foreignPaneAlive(t, socket, pane) {
		t.Fatalf("an unarmed model acted on the pane, mode = %v", m.mode)
	}
}

// A pane busy at startup is left alone until it rests, and a pane adopted
// later in the run is taken the same way, with no answer from anybody.
func TestAutoTakeoverWaitsForBusyPanesAndCoversLaterOnes(t *testing.T) {
	m := buildModel(t)
	m.adoptFirstDone = true
	// Each refresh reads the panes' screens, so it runs disarmed and the
	// status the test means is pinned before the armed pass.
	refresh := func(id, state string) {
		m.restoreArmed = false
		m.applyCmd(t, nil)
		setStatus(t, m, id, state)
		m.restoreArmed = true
	}
	socket, pane := adoptForeignPane(t, m, "busy", "busy", status.Working)
	refresh("busy", status.Working)
	if result := m.takeoverPass(); result.taken != 0 || result.owed != 1 {
		t.Fatalf("a working pane was taken: %+v", result)
	}
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("a working pane was ended")
	}
	setStatus(t, m, "busy", status.Idle)
	if result := m.takeoverPass(); result.taken != 1 || result.owed != 0 {
		t.Fatalf("the busy pane was not taken once idle: %+v", result)
	}
	if foreignPaneAlive(t, socket, pane) || !m.tmux.Exists("busy") {
		t.Fatal("the pane that went idle is not a board session")
	}

	// A pane the scan brings in later is taken the same way, unasked.
	socket, pane = adoptForeignPane(t, m, "late", "late", status.Idle)
	refresh("late", status.Idle)
	if result := m.takeoverPass(); result.taken != 1 {
		t.Fatalf("a pane adopted later in the run was not taken: %+v", result)
	}
	if foreignPaneAlive(t, socket, pane) || !m.tmux.Exists("late") {
		t.Fatal("the late pane is not a board session")
	}
}

// Keeping panes as they are is still a setting, and then nothing is taken
// until O asks for it.
func TestKeepAsIsSettingLeavesPanesForO(t *testing.T) {
	m := buildModel(t)
	m.restoreArmed = true
	m.adoptFirstDone = true
	setMode(t, m, outsidePanesSetting, paneAdopt)
	socket, pane := adoptForeignPane(t, m, "kept", "kept", status.Idle)
	m.applyCmd(t, nil)
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("keep as-is should leave the pane where it is")
	}
	pressKey(t, m, key("O"))
	if foreignPaneAlive(t, socket, pane) || !m.tmux.Exists("kept") {
		t.Fatal("O should take the kept pane over")
	}
}

// O ends the idle pane in its own window and brings the row back as a gi_
// session the manager owns, on the same id, name and directory.
func TestTakeoverRestartsAnIdlePaneAsAManagedSession(t *testing.T) {
	m := buildModel(t)
	socket, pane := adoptForeignPane(t, m, "borrowed", "borrowed", status.Idle)
	m.applyCmd(t, nil)

	pressKey(t, m, key("O"))
	if m.mode != modeList {
		t.Fatalf("O opened %v, want the pass to run without a card", m.mode)
	}
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

// A busy pane is not restarted on O: it is owed, and the refresh that first
// finds it idle is what takes it.
func TestTakeoverWaitsForABusyPaneToGoIdle(t *testing.T) {
	m := buildModel(t)
	socket, pane := adoptForeignPane(t, m, "busy", "busy", status.Working)
	m.applyCmd(t, nil)
	setStatus(t, m, "busy", status.Working)

	pressKey(t, m, key("O"))
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("a working pane was ended by O")
	}
	if !m.takeover.pending["busy"] {
		t.Fatal("the busy pane is not owed to the background pass")
	}
	if !strings.Contains(m.errBar.text, "1 waits until its pane is idle and unattended") {
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
	// Armed, the automatic pass does not queue it again either.
	m.restoreArmed = true
	m.takeoverPass()
	if m.takeover.pending["unread"] || !foreignPaneAlive(t, socket, pane) {
		t.Fatal("the automatic pass retried a pane the takeover refused")
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
