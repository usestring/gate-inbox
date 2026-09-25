package sessioncmd

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// TestEveryDeliberateEndIsRecordedAgainstTheAgentItEnded is the write half of
// the startup offer's rule: each way an operator ends a session outside the
// board -- the CLI's kill, the MCP tool's, an extension's board kill, an
// archive, a park -- leaves a mark naming the launch it ended, so the next
// start does not offer that session back as lost.
func TestEveryDeliberateEndIsRecordedAgainstTheAgentItEnded(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	create := func(name string) Session {
		t.Helper()
		created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: name, Tool: "stoppable"})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return created
	}
	cli, mcp, board, filed := create("cli"), create("mcp"), create("board"), create("filed")
	if _, err := h.sessions.Kill(h.caller.ID, cli.ID, extension.KillByCLI); err != nil {
		t.Fatalf("cli kill: %v", err)
	}
	if _, err := h.sessions.Kill(h.caller.ID, mcp.ID, extension.KillByMCP); err != nil {
		t.Fatalf("mcp kill: %v", err)
	}
	if _, err := h.sessions.BoardKill(board.ID); err != nil {
		t.Fatalf("board kill: %v", err)
	}
	if _, err := h.sessions.Archive(h.caller.ID, filed.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	parked := create("parked")
	if _, err := h.sessions.Park(h.caller.ID, false); err != nil {
		t.Fatalf("park: %v", err)
	}

	ends, err := h.store.SessionEnds()
	if err != nil {
		t.Fatalf("ends: %v", err)
	}
	for id, want := range map[string]string{
		cli.ID: store.EndKilled, mcp.ID: store.EndKilled, board.ID: store.EndKilled,
		filed.ID: store.EndArchived, parked.ID: store.EndParked,
	} {
		row, err := h.store.Get(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if end := ends[id]; end.Reason != want || !end.Matches(row) {
			t.Errorf("%s: end %+v, want %q for launch %v", row.Name, end, want, row.LaunchTime())
		}
	}
}

// TestALaunchedAgentsExitStatusIsRecorded proves the launch environment
// carries the exit file to every tool, not only the ones with hooks: this
// tool exits at once with status 0, which is what an operator's /exit leaves.
func TestALaunchedAgentsExitStatusIsRecorded(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "quitter", Tool: "echoer"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	manager := hooks.NewManager(h.sessions.configDir)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if code, _, ok := manager.ReadExit(created.ID); ok {
			if code != 0 {
				t.Fatalf("exit status %d, want 0", code)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no exit status was recorded")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
