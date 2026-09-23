package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/namesweep"
	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// TestLiveAdoptedPaneRenamesItself is the only proof that the directive works:
// a real agent, in a pane this test started by hand on a socket of its own,
// reading a sentence it never asked for and running the command in it. It
// spends tokens against whatever account the machine is signed in to, so it
// runs only when asked for by name.
//
//	GATE_INBOX_LIVE_SWEEP=1 GATE_INBOX_LIVE_SWEEP_BIN=/path/to/gate-inbox \
//	  go test ./internal/ui/ -run TestLiveAdoptedPaneRenamesItself -v -timeout 10m
func TestLiveAdoptedPaneRenamesItself(t *testing.T) {
	if os.Getenv("GATE_INBOX_LIVE_SWEEP") == "" {
		t.Skip("live sweep: set GATE_INBOX_LIVE_SWEEP=1 to run a real agent")
	}
	binary := os.Getenv("GATE_INBOX_LIVE_SWEEP_BIN")
	if binary == "" {
		t.Fatal("GATE_INBOX_LIVE_SWEEP_BIN must point at a built gate-inbox")
	}
	for _, tool := range []string{"tmux", "claude"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}

	// A socket of its own, so nothing here can reach the operator's board.
	socket := newTestSocket()
	cwd := t.TempDir()
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })

	// A pane somebody started by hand: no session-id flag, no settings file,
	// no manager environment. Exactly what adoption finds.
	create := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", "byhand",
		"-c", cwd, "-x", "160", "-y", "48", "claude", "reply with the single word ok and stop")
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("start the pane: %v: %s", err, out)
	}
	paneID := strings.TrimSpace(tmuxOut(t, socket, "display-message", "-p", "-t", "byhand", "#{pane_id}"))
	if paneID == "" {
		t.Fatal("no pane id")
	}
	// A directory Claude Code has never seen opens on its trust dialog, which
	// is a fixture problem rather than the thing under test: the sweep would
	// read that pane as waiting and refuse it, which is the correct answer and
	// not the one this test is here to get.
	answerTrustDialog(t, socket, "byhand")

	configDir := t.TempDir()
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	sessionID := "1b364bb3"
	sess := store.Session{
		ID: sessionID, Name: "by-hand", Tool: "claude", Cwd: cwd,
		Status: status.Idle, TmuxSocket: socket, TmuxPaneID: paneID,
	}
	if err := st.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	driver := newTestDriver(t, testSocket+"livemgr")
	t.Cleanup(func() { driver.Release(sessionID) })
	if err := driver.Adopt(sessionID, tmux.Target{Socket: socket, Name: paneID}); err != nil {
		t.Fatal(err)
	}

	// Wait for the agent's first turn to land, which is what writes the cache
	// numbers the warm gate reads.
	reader := promptcache.NewReader(promptcache.DefaultRoot())
	var cache promptcache.State
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		if cache = reader.Lookup(cwd, ""); cache.Known {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !cache.Known {
		t.Fatalf("no transcript for %s after four minutes", cwd)
	}
	t.Logf("cache: ttl=%v age=%v ctx=%s transcript=%s",
		cache.TTL, cache.Age(time.Now()).Round(time.Second),
		namesweep.Tokens(cache.ContextTokens), cache.Transcript)

	hookManager := hooks.NewManager(configDir)
	directive := func(c namesweep.Candidate) string {
		return launch.AdoptedRenameDirective(launch.AdoptedRenameCommand(binary, configDir, c.ID))
	}
	candidate := namesweep.Candidate{
		ID: sessionID, Name: sess.Name, Tool: "claude", Cwd: cwd,
		Status: status.Idle, Adopted: true, CacheReadable: true,
	}
	plan := namesweep.Build([]namesweep.Candidate{candidate},
		func(c namesweep.Candidate) promptcache.State { return reader.Lookup(c.Cwd, "") },
		directive, time.Now())
	if len(plan.Targets) != 1 {
		t.Fatalf("the pane is idle and warm but was not a target: %+v", plan.Skipped)
	}
	t.Logf("target reason: %s", plan.Targets[0].Reason)
	t.Logf("directive: %s", plan.Targets[0].Directive)

	result := namesweep.Send(plan.Targets, liveGate{driver: driver, reader: reader},
		driver.SendText, time.Now, 0, time.Sleep, nil)
	if len(result.Sent) != 1 {
		t.Fatalf("nothing was sent: held=%+v err=%v", result.Held, result.Err)
	}

	// The rename subcommand writes a name file the manager's poll reads back.
	// Its appearance is the agent having run the command it was handed.
	deadline = time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if name, ok := hookManager.ReadName(sessionID); ok {
			t.Logf("the agent named itself %q", name)
			return
		}
		time.Sleep(2 * time.Second)
	}
	pane, _ := driver.CapturePane(sessionID)
	t.Fatalf("no name after three minutes; pane:\n%s", pane)
}

// liveGate is the sweep's own gate over a real pane, without the caret check
// the TUI adds, since nobody is typing into this one.
type liveGate struct {
	driver *tmux.Driver
	reader *promptcache.Reader
}

func (g liveGate) Status(target namesweep.Verdict) (string, error) {
	if !g.driver.Exists(target.ID) {
		return status.Dead, nil
	}
	return status.Idle, nil
}

func (g liveGate) Cache(target namesweep.Verdict) promptcache.State {
	return g.reader.Lookup(target.Cwd, "")
}

// answerTrustDialog accepts the first-run folder-trust prompt if it is up.
func answerTrustDialog(t *testing.T, socket, target string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		pane := tmuxOut(t, socket, "capture-pane", "-p", "-t", target)
		if strings.Contains(pane, "trust this folder") {
			tmuxOut(t, socket, "send-keys", "-t", target, "Enter")
			return
		}
		if strings.Contains(pane, "auto mode") || strings.Contains(pane, "?") {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func tmuxOut(t *testing.T, socket string, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", append([]string{"-L", socket}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return string(out)
}
