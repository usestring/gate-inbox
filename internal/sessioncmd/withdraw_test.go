package sessioncmd

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

// A withdrawn spawn leaves nothing behind: no pane still running the agent,
// and no row on the board for anyone to find working on the task.
func TestWithdrawRemovesTheSpawnsPaneAndRow(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "turned-down", Prompt: "do the thing"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !h.driver.Exists(created.ID) {
		t.Fatal("the spawn's pane never opened")
	}
	if err := h.sessions.Withdraw(h.caller.ID, created.ID); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if h.driver.Exists(created.ID) {
		t.Fatal("the withdrawn spawn's pane is still running")
	}
	if _, err := h.store.Get(created.ID); err == nil {
		t.Fatal("the withdrawn spawn's row is still on the board")
	}
}

// Withdraw is the spawner's undo, never a delete for any session.
func TestWithdrawRefusesASessionTheCallerDidNotSpawn(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	other := store.Session{ID: "0ther000", Name: "someone-else", Tool: "echoer", Cwd: h.caller.Cwd, Group: h.caller.Group}
	if err := h.store.CreateSession(other); err != nil {
		t.Fatal(err)
	}
	if err := h.sessions.Withdraw(h.caller.ID, other.ID); err == nil {
		t.Fatal("withdrew a session the caller never spawned")
	}
	if _, err := h.store.Get(other.ID); err != nil {
		t.Fatalf("the other session's row went: %v", err)
	}
}
