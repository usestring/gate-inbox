package sessioncmd

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// BoardMigrateAccount moves a session to a new one on the named account
// with no calling session, and the new row takes over what the old one
// held: its children, its priority and its seat on the board. The old one
// is archived on its own account with its pane ended.
func TestBoardMigrateAccountHandsTheSessionToANewRowOnTheAccount(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)
	if err := h.store.SetAccount(source.ID, "OLD"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetPriority(source.ID, priority.Urgent); err != nil {
		t.Fatal(err)
	}
	child := store.Session{ID: uuid.NewString()[:8], Name: "worker", Tool: "echoer", Cwd: source.Cwd, Group: source.Group, Status: status.Idle, ParentID: source.ID, SpawnedBy: source.ID}
	if err := h.store.CreateSession(child); err != nil {
		t.Fatal(err)
	}
	if err := h.driver.Create(source.ID, source.Cwd, "cat", nil, 80, 24); err != nil {
		t.Fatal(err)
	}

	moved, err := h.sessions.BoardMigrateAccount(source.ID, "alice1")
	if err != nil {
		t.Fatal(err)
	}
	row, err := h.store.Get(moved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ID == source.ID || row.MigrationID != source.ID || row.Account != "ALICE1" || row.Tool != "claude" {
		t.Fatalf("new row = %+v, want a migration of %s on ALICE1", row, source.ID)
	}
	if row.Priority != priority.Urgent {
		t.Fatalf("new row's priority = %q, want urgent", row.Priority)
	}
	kid, err := h.store.Get(child.ID)
	if err != nil || kid.ParentID != moved.ID || kid.SpawnedBy != moved.ID {
		t.Fatalf("child after the move = %+v %v, want it under %s", kid, err, moved.ID)
	}
	old, err := h.store.Get(source.ID)
	if err != nil || old.Account != "OLD" || !old.Archived || h.driver.Exists(source.ID) {
		t.Fatalf("source not retired on its own account: %+v %v", old, err)
	}
	waitForUnwrappedOutput(t, h.sessions, h.caller.ID, moved.ID, "continue the work")
}

// It refuses the account the session is already on, and a pane the manager
// did not start, leaving the session where it was.
func TestBoardMigrateAccountRefusesTheSameAccountAndAnAdoptedPane(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)
	if err := h.store.SetAccount(source.ID, "ALICE1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.BoardMigrateAccount(source.ID, "alice1"); err == nil || !strings.Contains(err.Error(), "already runs on") {
		t.Fatalf("a move to the same account = %v, want refused", err)
	}
	adopted := store.Session{ID: uuid.NewString()[:8], Name: "adopted", Tool: "claude", Cwd: source.Cwd, Group: source.Group, Status: status.Idle, AgentSessionID: "conv-adopted", TmuxPaneID: "%99"}
	if err := h.store.CreateSession(adopted); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.BoardMigrateAccount(adopted.ID, "bob1"); err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("an adopted pane = %v, want refused", err)
	}
	for _, id := range []string{source.ID, adopted.ID} {
		row, err := h.store.Get(id)
		if err != nil || row.Archived || row.MigrationID != "" {
			t.Fatalf("refused move changed %s: %+v %v", id, row, err)
		}
	}
}
