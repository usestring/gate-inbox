package sessioncmd

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/git"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// A pane the board adopted is reached from its row alone. Each command here
// opens its own driver, as a CLI or MCP process does with no board running,
// so nothing one command registers is left for the next to lean on.
func TestAdoptedRowIsReadSentAndKilledWithNoBoard(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	socket := h.driver.SocketName()
	sessions := newSessions(h.sessions.configDir, MCPVocabulary(), func(string) (*tmux.Driver, error) {
		return tmux.NewWithSocket(socket)
	}, git.New)

	paneID, _ := newForeignPane(t, socket)
	if out, err := exec.Command("tmux", "-L", socket, "send-keys", "-t", paneID, "echo adopted-marker", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("type into the foreign pane: %v: %s", err, out)
	}
	adopted := store.Session{ID: uuid.NewString()[:8], Name: "borrowed", Tool: "stoppable", Cwd: h.caller.Cwd, Status: status.Idle,
		TmuxSocket: socket, TmuxPaneID: paneID}
	if err := h.store.CreateSession(adopted); err != nil {
		t.Fatalf("create adopted row: %v", err)
	}

	screen := waitForSessionOutput(t, sessions, h.caller.ID, adopted.ID, "adopted-marker")
	if screen.Mode != "pane" {
		t.Fatalf("read mode = %q, want the live pane", screen.Mode)
	}

	sent, err := sessions.Send(h.caller.ID, adopted.ID, "hello", "", false)
	if err != nil {
		t.Fatalf("Send to the adopted row: %v", err)
	}
	if queued, _ := h.store.QueuedCount(adopted.ID); sent.MessageID == 0 || queued != 1 {
		t.Fatalf("send = %+v with %d queued, want one message queued", sent, queued)
	}

	killed, err := sessions.Kill(h.caller.ID, adopted.ID, extension.KillByCLI)
	if err != nil {
		t.Fatalf("Kill the adopted row: %v", err)
	}
	if killed.Status != status.Dead {
		t.Fatalf("killed status = %q", killed.Status)
	}
	assertAdoptedKilled(t, h.store, socket, adopted, paneID)
}

// assertAdoptedKilled checks a kill of an adopted row did what a kill of a
// started session does: the agent's pane is gone, its last screen kept, the
// row dead, and the end recorded as the operator's.
func assertAdoptedKilled(t *testing.T, st *store.Store, socket string, adopted store.Session, paneID string) {
	t.Helper()
	if foreignPaneExists(t, socket, paneID) {
		t.Fatalf("pane %s is still running after its row was killed", paneID)
	}
	row, err := st.Get(adopted.ID)
	if err != nil {
		t.Fatalf("get adopted row: %v", err)
	}
	if row.Status != status.Dead {
		t.Fatalf("adopted row status = %q, want dead", row.Status)
	}
	if snapshot, _ := st.Snapshot(adopted.ID); !strings.Contains(snapshot, "adopted-marker") {
		t.Fatalf("kill kept no last screen: %q", snapshot)
	}
	ends, err := st.SessionEnds()
	if err != nil {
		t.Fatalf("session ends: %v", err)
	}
	if end := ends[adopted.ID]; end.Reason != store.EndKilled || !end.Matches(row) {
		t.Fatalf("recorded end = %+v, want %q for the agent the row holds", end, store.EndKilled)
	}
}
