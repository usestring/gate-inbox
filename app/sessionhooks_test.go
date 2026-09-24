package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// envEchoTool writes why it was launched where the fixture's extension told
// it to, then stays up like an agent would.
const envEchoTool = `
[tools.envecho]
command = "sh -c 'printf %s \"$NOOP_LAUNCH\" > \"$NOOP_OUT\"; sleep 30' --"
default_status = "idle"
activity_cutoff = "(?m)^\\$ "
`

// An extension compiled outside this module has a say in the sessions a
// session launches through the CLI: it refuses one spawn, adds its own
// environment to the next, and follows a migration to the session that
// carries it on.
func TestExternalBuildHasASayInLaunches(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("a spawn launches a tmux pane")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("hooks")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+envEchoTool)
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	env = append(env, "GATE_INBOX_SESSION_ID=ca11e400")
	seedSessions(t, filepath.Join(home, "state.db"))
	work := t.TempDir()
	seedOpenCodeSource(t, filepath.Join(home, "state.db"), work)
	data := filepath.Join(home, "extensions", "noop")

	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("spawn", "--tool", "envecho", "--name", "over-budget", "--directory", work)
	if err == nil || !strings.Contains(out, `extension "noop" refused the spawn: this goal's budget is spent`) {
		t.Fatalf("an over-budget spawn: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(data, "spawned.txt")); err == nil {
		t.Fatal("a refused spawn was reported as launched")
	}

	out, err = run("spawn", "--tool", "envecho", "--name", "within-budget", "--directory", work, "--json")
	if err != nil {
		t.Fatalf("spawn: %v\n%s", err, out)
	}
	spawned := idOf(t, out)
	if got := readEventually(t, filepath.Join(data, "env-"+spawned+".txt")); got != "spawn" {
		t.Fatalf("the spawned pane saw NOOP_LAUNCH=%q, want the extension's spawn", got)
	}
	if got := readEventually(t, filepath.Join(data, "spawned.txt")); got != spawned+" session \n" {
		t.Fatalf("spawned.txt = %q", got)
	}

	out, err = run("spawn", "--tool", "envecho", "--name", "detached", "--nest=false", "--prompt", "the sub-task", "--directory", work, "--json")
	if err != nil {
		t.Fatalf("spawn detached: %v\n%s", err, out)
	}
	detached := storedSession(t, filepath.Join(home, "state.db"), idOf(t, out))
	if detached.ParentID != "ca11e400" || !strings.Contains(detached.LaunchPrompt, "NOOP GOAL for ca11e400\n\nthe sub-task") {
		t.Fatalf("the shaped spawn was filed under %q on %q", detached.ParentID, detached.LaunchPrompt)
	}

	out, err = run("migrate", "0de0c0de", "--tool", "envecho", "--json")
	if err != nil {
		t.Fatalf("migrate: %v\n%s", err, out)
	}
	moved := idOf(t, out)
	if got := readEventually(t, filepath.Join(data, "migrated.txt")); got != "0de0c0de>"+moved+"\n" {
		t.Fatalf("migrated.txt = %q", got)
	}
	if got := readEventually(t, filepath.Join(data, "env-"+moved+".txt")); got != "migrate 0de0c0de" {
		t.Fatalf("the migrated pane saw NOOP_LAUNCH=%q", got)
	}
	if prompt := storedSession(t, filepath.Join(home, "state.db"), moved).LaunchPrompt; !strings.Contains(prompt, "NOOP GOAL carried from 0de0c0de\n\n") {
		t.Fatalf("the migration launched on %q, want the extension's brief", prompt)
	}
}

// seedOpenCodeSource is a session whose conversation can move without a
// transcript on disk: opencode's is read by a command.
func seedOpenCodeSource(t *testing.T, path, cwd string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(store.Session{
		ID: "0de0c0de", Name: "the source", Tool: "opencode", Status: "dead",
		Cwd: cwd, AgentSessionID: "ses_fixture", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func storedSession(t *testing.T, path, id string) store.Session {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sess, err := st.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func idOf(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, `"id": "`)
	if !ok {
		if _, rest, ok = strings.Cut(out, `"id":"`); !ok {
			t.Fatalf("no id in %s", out)
		}
	}
	id, _, _ := strings.Cut(rest, `"`)
	return id
}

func readEventually(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		body, err := os.ReadFile(path)
		if err == nil && len(body) > 0 {
			return string(body)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never written: %v", filepath.Base(path), err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
