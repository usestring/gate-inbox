package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// The CLI runs with no board, so a pane the board adopted is known to it
// only by its row. read, send and kill each reach that pane from the row.
func TestReadSendAndKillReachAnAdoptedPaneWithNoBoard(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	configDir := tmuxtest.ScratchDir(t)
	socket := tmuxtest.Socket(t, "cliadopt")
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
	caller := store.Session{ID: "cafe0001", Name: "caller", Tool: "agent", Cwd: t.TempDir(), Status: status.Idle}
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
	adopted := store.Session{ID: "beef1234", Name: "borrowed", Tool: "agent", Cwd: caller.Cwd, Status: status.Idle,
		TmuxSocket: socket, TmuxPaneID: paneID}
	if err := st.CreateSession(adopted); err != nil {
		t.Fatal(err)
	}
	sessions := sessioncmd.NewSessions(configDir, sessioncmd.CLIVocabulary())

	var out bytes.Buffer
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(out.String(), "adopted-marker") {
		if time.Now().After(deadline) {
			t.Fatalf("read never showed the adopted pane: %q", out.String())
		}
		out.Reset()
		if err := runRead(&out, sessions, []string{adopted.ID}, caller.ID); err != nil {
			t.Fatalf("read: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := runSend(&out, sessions, []string{adopted.ID, "hello"}, caller.ID); err != nil {
		t.Fatalf("send: %v", err)
	}
	if queued, _ := st.QueuedCount(adopted.ID); queued != 1 {
		t.Fatalf("send queued %d messages, want 1", queued)
	}
	if err := runKill(&out, sessions, []string{adopted.ID}, caller.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	// The server goes with its last pane, so a failed listing is a gone pane.
	panes, _ := exec.Command("tmux", "-L", socket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if slices.Contains(strings.Fields(string(panes)), paneID) {
		t.Fatalf("pane %s still runs after kill", paneID)
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
		t.Fatalf("after kill: status %q, end %+v", row.Status, ends[adopted.ID])
	}
}
