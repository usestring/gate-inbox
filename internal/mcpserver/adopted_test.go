package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// A session's MCP server runs with no board, so a pane the board adopted is
// known to it only by its row. read_session, send_session and kill_session
// each reach that pane from the row.
func TestSessionToolsReachAnAdoptedPaneWithNoBoard(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	configDir := tmuxtest.ScratchDir(t)
	socket := tmuxtest.Socket(t, "mcpadopt")
	t.Setenv(tmux.SocketEnv, socket)
	t.Cleanup(func() {
		tmuxtest.KillServer(socket)
		tmuxtest.ReapSocket(socket)
	})
	config := "[tools.agent]\ncommand = \"cat\"\ndefault_status = \"idle\"\nactivity_cutoff = \"(?m)^\\u276f\"\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	caller := store.Session{ID: "abcdef01", Name: "caller", Tool: "agent", Cwd: t.TempDir(), Status: status.Idle}
	if err := st.CreateSession(caller); err != nil {
		t.Fatal(err)
	}
	tmuxCmd := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-L", socket}, args...)...).Output()
		if err != nil {
			t.Fatalf("tmux %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	paneID := tmuxCmd("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "foreign", "-x", "80", "-y", "24", "sh")
	tmuxCmd("send-keys", "-t", paneID, "echo adopted-marker", "Enter")
	adopted := store.Session{ID: "a1b2c3d4", Name: "borrowed", Tool: "agent", Cwd: caller.Cwd, Status: status.Idle,
		TmuxSocket: socket, TmuxPaneID: paneID}
	if err := st.CreateSession(adopted); err != nil {
		t.Fatal(err)
	}
	session := connect(t, configDir, caller.ID)

	deadline := time.Now().Add(3 * time.Second)
	for {
		text, isError := callText(t, session, "read_session", map[string]any{"session_id": adopted.ID})
		if !isError && strings.Contains(text, "adopted-marker") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("read_session never showed the adopted pane: %q", text)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if text, isError := callText(t, session, "send_session", map[string]any{"session_id": adopted.ID, "message": "hello"}); isError {
		t.Fatalf("send_session: %s", text)
	}
	if queued, _ := st.QueuedCount(adopted.ID); queued != 1 {
		t.Fatalf("send_session queued %d messages, want 1", queued)
	}
	if text, isError := callText(t, session, "kill_session", map[string]any{"session_id": adopted.ID}); isError {
		t.Fatalf("kill_session: %s", text)
	}
	// The server goes with its last pane, so a failed listing is a gone pane.
	panes, _ := exec.Command("tmux", "-L", socket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if slices.Contains(strings.Fields(string(panes)), paneID) {
		t.Fatalf("pane %s still runs after kill_session", paneID)
	}
	row, err := st.Get(adopted.ID)
	if err != nil {
		t.Fatal(err)
	}
	ends, err := st.SessionEnds()
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != status.Dead || ends[adopted.ID].Reason != store.EndKilled {
		t.Fatalf("after kill_session: status %q, end %+v", row.Status, ends[adopted.ID])
	}
}
