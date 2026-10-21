package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// A claude the board adopted has no GATE_INBOX_SESSION_ID, so its shell
// commands find their row through the pane's adoption marker; a launched
// session's environment still wins.
func TestCallerIDFallsBackToTheAdoptionMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(hooks.EnvSessionID, "")
	t.Setenv("TMUX", "/run/tmux-test/default,4242,0")
	t.Setenv("TMUX_PANE", "%7")
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID with no marker = %q, want none", got)
	}
	if err := hooks.NewManager(dir).SyncAdopted([]hooks.AdoptedPane{
		{ID: "a1b2c3d4", ServerPID: 4242, PaneID: "%7", AgentPID: os.Getppid()},
	}); err != nil {
		t.Fatal(err)
	}
	if got := callerID(dir); got != "a1b2c3d4" {
		t.Fatalf("callerID in an adopted pane = %q, want a1b2c3d4", got)
	}
	t.Setenv(hooks.EnvSessionID, "launched")
	if got := callerID(dir); got != "launched" {
		t.Fatalf("callerID with a launch's id = %q, want launched", got)
	}
	t.Setenv(hooks.EnvSessionID, "")
	t.Setenv("TMUX_PANE", "")
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID outside tmux = %q, want none", got)
	}
}

// An adopted opencode or codex pane leaves no adoption marker, so the board
// row that owns the pane is the only record of which session a command in it
// speaks as.
func TestCallerIDFallsBackToTheAdoptedPaneRow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(hooks.EnvSessionID, "")
	t.Setenv("TMUX", "/run/tmux-test/default,4242,0")
	t.Setenv("TMUX_PANE", "%7")
	serverStarted := time.Now().Add(-time.Hour)
	stubPaneServer(t, func(_, pid string) (time.Time, bool) {
		return serverStarted, pid == "4242"
	})

	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.CreateSession(store.Session{
		ID: "0a75de01", Name: "adopted", Tool: "opencode", Cwd: "/tmp", Status: "idle",
		TmuxSocket: "default", TmuxPaneID: "%7",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	if got := callerID(dir); got != "0a75de01" {
		t.Fatalf("callerID in an adopted pane = %q, want 0a75de01", got)
	}
	t.Setenv("TMUX_PANE", "%8")
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID in another pane = %q, want none", got)
	}
	t.Setenv("TMUX", "/run/tmux-test/other,5151,0")
	t.Setenv("TMUX_PANE", "%7")
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID on another server = %q, want none", got)
	}

	// The server restarted on the same socket and handed out %7 again: the
	// row was adopted on the old one and must not answer for the new pane.
	t.Setenv("TMUX", "/run/tmux-test/default,6262,0")
	serverStarted = time.Now().Add(time.Minute)
	stubPaneServer(t, func(_, pid string) (time.Time, bool) {
		return serverStarted, pid == "6262"
	})
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID on a restarted server = %q, want none", got)
	}
	stubPaneServer(t, func(string, string) (time.Time, bool) { return time.Time{}, false })
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID with the server unreadable = %q, want none", got)
	}
}

func stubPaneServer(t *testing.T, fn func(pane, serverPID string) (time.Time, bool)) {
	t.Helper()
	prev := paneServerStarted
	paneServerStarted = fn
	t.Cleanup(func() { paneServerStarted = prev })
}

// The socket path is the one $TMUX field that may hold a comma, so the name
// is read with the pid and session cut from the right.
func TestTmuxSocketNameKeepsACommaInThePath(t *testing.T) {
	for _, tc := range []struct{ env, socket, pid string }{
		{"/run/tmux-test/default,4242,0", "default", "4242"},
		{"/run/tmux-test/team,blue,4242,0", "team,blue", "4242"},
		{"/run/a,b/default,4242,0", "default", "4242"},
		{"", "", ""},
		{"/run/tmux-test/default", "", ""},
	} {
		if socket, pid := tmuxSocketName(tc.env); socket != tc.socket || pid != tc.pid {
			t.Errorf("tmuxSocketName(%q) = %q, %q; want %q, %q", tc.env, socket, pid, tc.socket, tc.pid)
		}
	}
}
