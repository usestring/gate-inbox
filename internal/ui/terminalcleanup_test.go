package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// parentKidShell is a parent, a child placed under it, and a live terminal
// opened with T on the child's row, which nests under the child.
func parentKidShell(t *testing.T) (*Model, store.Session, store.Session, store.Session) {
	t.Helper()
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "parent", dir, "")
	createSession(t, m, "kid", dir, "")
	loadStoredRows(t, m)
	var parent, kid store.Session
	for _, sess := range m.sessions {
		switch sess.Name {
		case "parent":
			parent = sess
		case "kid":
			kid = sess
		}
	}
	if err := m.store.PlaceSession(kid.ID, parent.Group, parent.ID); err != nil {
		t.Fatalf("PlaceSession: %v", err)
	}
	loadStoredRows(t, m)
	m.setChildrenFolded(parent.ID, false)
	m.rebuildRows()
	m.selectSessionRow(t, "kid")
	shell := spawnTerminal(t, m)
	if shell.ParentID != kid.ID {
		t.Fatalf("T on the child filed the terminal under %q, want %q", shell.ParentID, kid.ID)
	}
	loadStoredRows(t, m)
	parent, _ = m.sessionByID(parent.ID)
	kid, _ = m.sessionByID(kid.ID)
	return m, parent, kid, shell
}

func ids(sessions []store.Session) map[string]bool {
	out := make(map[string]bool, len(sessions))
	for _, sess := range sessions {
		out[sess.ID] = true
	}
	return out
}

// x on a parent ends the child and, with it, the terminal nested under the
// child. It used to read only the parent's direct children, so the child
// went and its shell kept running under a row that had been filed away.
func TestArchivingAParentTakesTheTerminalUnderItsChild(t *testing.T) {
	m, parent, kid, shell := parentKidShell(t)
	target, ok := m.archiveConfirmFor(parent)
	if !ok {
		t.Fatalf("archiveConfirmFor: %q", m.errBar.text)
	}
	if set := ids(target.sessions); !set[kid.ID] || !set[shell.ID] {
		t.Fatalf("archive set = %v, want the child and its terminal", target.sessions)
	}
	m.confirm = target
	if text := m.archiveConfirmed(m.livePanes()); text != "" {
		t.Fatalf("archive: %s", text)
	}
	if m.tmux.Exists(shell.ID) {
		t.Fatal("the child's terminal is still running after its parent was archived")
	}
	row, err := m.store.Get(shell.ID)
	if err != nil || !row.Archived {
		t.Fatalf("terminal row = %+v, %v; want archived", row, err)
	}
}

// A fan-out launcher run in that terminal opens its agents as windows of the
// terminal's own tmux session. Archiving the parent ends the terminal and
// leaves them: they were never the manager's to end.
func TestArchivingAParentLeavesWindowsOpenedInItsTerminal(t *testing.T) {
	m, parent, _, shell := parentKidShell(t)
	out, err := tmuxCmd("new-window", "-d", "-t", "="+tmux.SessionName(shell.ID), "-P", "-F", "#{pane_id}", "cat").CombinedOutput()
	if err != nil {
		t.Fatalf("new-window in the terminal: %v: %s", err, out)
	}
	stray := strings.TrimSpace(string(out))
	t.Cleanup(func() { tmuxCmd("kill-pane", "-t", stray).Run() })

	target, ok := m.archiveConfirmFor(parent)
	if !ok {
		t.Fatalf("archiveConfirmFor: %q", m.errBar.text)
	}
	m.confirm = target
	if text := m.archiveConfirmed(m.livePanes()); text != "" {
		t.Fatalf("archive: %s", text)
	}
	if m.tmux.Exists(shell.ID) {
		t.Fatal("the terminal is still running after its parent was archived")
	}
	if out, err := tmuxCmd("display-message", "-p", "-t", stray, "#{pane_id}").Output(); err != nil || strings.TrimSpace(string(out)) != stray {
		t.Fatalf("archiving the parent ended %s, a window opened in its terminal", stray)
	}
}

// k keeps the spawned children running, and a kept child keeps its shell.
func TestKeepingAChildKeepsTheTerminalUnderIt(t *testing.T) {
	m, parent, kid, shell := parentKidShell(t)
	target, ok := m.archiveConfirmFor(parent)
	if !ok {
		t.Fatalf("archiveConfirmFor: %q", m.errBar.text)
	}
	target.keepChildren = true
	m.confirm = target
	if text := m.archiveConfirmed(m.livePanes()); text != "" {
		t.Fatalf("archive: %s", text)
	}
	for _, sess := range []store.Session{kid, shell} {
		if !m.tmux.Exists(sess.ID) {
			t.Fatalf("%s was ended though its child was kept", sess.Name)
		}
		if row, err := m.store.Get(sess.ID); err != nil || row.Archived {
			t.Fatalf("%s row = %+v, %v; want active", sess.Name, row, err)
		}
	}
}

// The exited-child sweep ends nothing that is running, so a live terminal
// under an exited child holds the child on the list; once the shell exits
// too, both are filed together rather than the shell being left loose.
func TestTheChildSweepFilesAnExitedChildWithItsTerminal(t *testing.T) {
	m, parent, kid, shell := parentKidShell(t)
	id, _, err := m.store.Enqueue(store.InboxMessage{
		SessionID: parent.ID, SenderID: kid.ID, SenderName: kid.Name,
		Body: "done", SentAt: time.Now(),
	}, store.DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := m.store.MarkDelivered(id, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if err := m.tmux.Kill(kid.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := m.store.UpdateStatus(kid.ID, status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)

	runChildSweep(t, m)
	if !m.tmux.Exists(shell.ID) {
		t.Fatal("the sweep ended a live terminal")
	}
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived {
		t.Fatalf("the sweep filed the child out from under its live terminal: %+v, %v", row, err)
	}

	if err := m.tmux.Kill(shell.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := m.store.UpdateStatus(shell.ID, status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)
	runChildSweep(t, m)
	for _, sess := range []store.Session{kid, shell} {
		if row, err := m.store.Get(sess.ID); err != nil || !row.Archived {
			t.Fatalf("%s row = %+v, %v; want archived", sess.Name, row, err)
		}
	}
}
