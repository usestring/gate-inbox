package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestStopFromInsidePaneCompletesLifecycleOutsideIt(t *testing.T) {
	testSelfStop(t, true, false)
}

// The last session's death empties the server, and tmux SIGTERMs a
// run-shell -b job on the way out, so the worker must outlive that.
func TestStopOfTheServersLastSessionCompletesLifecycle(t *testing.T) {
	testSelfStop(t, false, false)
}

func TestStopDiscoversAdoptedCallerWithoutLaunchEnvironment(t *testing.T) {
	testSelfStop(t, true, true)
}

func testSelfStop(t *testing.T, withSentinel, adopted bool) {
	bin := buildFixture(t)
	socket := tmuxtest.Socket(t, "selfstop")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+promptTool)
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	env = append(withoutKey(env, "GATE_INBOX_SESSION_ID"), "GATE_INBOX_SESSION_ID=ca11e400")
	spawn := func(name string) string {
		t.Helper()
		cmd := exec.Command(bin, "spawn", "--tool", "prompter", "--name", name, "--directory", t.TempDir(), "--json")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("spawn: %v\n%s", err, out)
		}
		return idOf(t, string(out))
	}
	target := spawn("self-stop")
	var sentinel string
	if withSentinel {
		sentinel = spawn("sentinel")
	}
	tmuxCommand := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	row, err := st.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(home, "history.jsonl")
	if err := os.WriteFile(history, []byte("preserved conversation"), 0o600); err != nil {
		t.Fatal(err)
	}
	row.AgentSessionID = "preserved-conversation"
	if err := st.SetAgentSessionID(target, row.AgentSessionID); err != nil {
		t.Fatal(err)
	}
	dry := filepath.Join(home, "dry.json")
	pane := tmuxCommand("display-message", "-p", "-t", "gi_"+target, "#{pane_id}")
	command := tmux.ShellQuote(bin) + " stop --dry-run --json > " + tmux.ShellQuote(dry) + "; " + tmux.ShellQuote(bin) + " stop --json"
	if adopted {
		db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec("UPDATE sessions SET tmux_socket = ?, tmux_pane_id = ? WHERE id = ?", socket, pane, target); err != nil {
			t.Fatal(err)
		}
	}
	// respawn-pane puts the command inside the actual managed surface, so
	// this catches lifecycle writes accidentally left in the killed process.
	launchEnv := "env GATE_INBOX_HOME=" + tmux.ShellQuote(home) + " GATE_INBOX_SESSION_ID=" + tmux.ShellQuote(target)
	if adopted {
		launchEnv = "env -u GATE_INBOX_SESSION_ID GATE_INBOX_HOME=" + tmux.ShellQuote(home)
	}
	tmuxCommand("respawn-pane", "-k", "-t", pane, launchEnv+" sh -c "+tmux.ShellQuote(command))
	deadline := time.Now().Add(15 * time.Second)
	var ends map[string]store.SessionEnd
	for time.Now().Before(deadline) {
		ends, err = st.SessionEnds()
		if err != nil {
			t.Fatal(err)
		}
		if ends[target].Reason == store.EndKilled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	kept, err := st.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	if !ends[target].Matches(kept) || kept.Status != "dead" || kept.Archived || kept.AgentSessionID != row.AgentSessionID {
		dryData, _ := os.ReadFile(dry)
		logData, _ := os.ReadFile(filepath.Join(home, "logs", "gate-inbox.log"))
		t.Logf("dry: %s; log: %s", dryData, logData)
		capture := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", pane)
		capture.Env = env
		captured, _ := capture.CombinedOutput()
		t.Logf("pane: %s", captured)
		t.Fatalf("end: %+v, row: %+v", ends[target], kept)
	}
	if withSentinel {
		if got := tmuxCommand("list-sessions", "-F", "#{session_name}"); strings.Contains(got, "gi_"+target) || !strings.Contains(got, "gi_"+sentinel) {
			t.Fatalf("remaining sessions: %s", got)
		}
	}
	data, err := os.ReadFile(dry)
	if err != nil || !json.Valid(data) {
		t.Fatalf("dry run: %s, %v", data, err)
	}
	if data, err := os.ReadFile(history); err != nil || string(data) != "preserved conversation" {
		t.Fatal("conversation was removed")
	}
	want := fmt.Sprintf("%s cli %s dead\n", target, target)
	if got := readEventually(t, filepath.Join(home, "extensions", "noop", "kills.txt")); got != want {
		t.Fatalf("kill observer: %q, want %q", got, want)
	}
}

func TestStopRefusesSharedDaemonEvenWithoutThreadOrManagedEnvironment(t *testing.T) {
	bin := buildFixture(t)
	socket := tmuxtest.Socket(t, "sharedstop")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+promptTool)
	home := envValue(env, "GATE_INBOX_HOME")
	seedSessions(t, filepath.Join(home, "state.db"))
	env = withoutKey(withoutKey(env, "GATE_INBOX_SESSION_ID"), "CODEX_THREAD_ID")
	tmuxCmd := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	tmuxCmd("new-session", "-d", "-s", "sentinel", "sleep 60")
	pane := tmuxCmd("display-message", "-p", "-t", "sentinel", "#{pane_id}")
	state, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	row := store.Session{ID: "daem0001", Tool: "codex", Name: "adopted", Status: "working", CreatedAt: time.Now(), TmuxSocket: socket, TmuxPaneID: pane, AgentSessionID: "unverified-thread"}
	if err := state.CreateSession(row); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "codex")
	build := exec.Command("go", "build", "-o", fake, "./testdata/stopcaller")
	build.Env = tmuxtest.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	for _, thread := range []string{"", "unverified-thread", "another-thread"} {
		output := filepath.Join(t.TempDir(), "result.json")
		cmd := exec.Command(fake, "app-server", bin, output)
		cmd.Env = append(env, "CODEX_THREAD_ID="+thread, "TMUX_PANE="+pane)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		data := readEventually(t, output)
		if !strings.Contains(data, "cannot verify a Gate Inbox caller pane") || !strings.Contains(data, "\"Failed\":true") {
			t.Fatalf("unsafe daemon classification: %s", data)
		}
		if got := tmuxCmd("display-message", "-p", "-t", pane, "#{pane_id}"); got != pane {
			t.Fatal("sentinel pane ended")
		}
	}
	ends, err := state.SessionEnds()
	if err != nil || len(ends) != 0 {
		t.Fatalf("refusal wrote lifecycle: %v, %v", ends, err)
	}
}
