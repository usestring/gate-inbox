package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
// environment to the next, refuses a third by counting the board,
// follows a migration to the session that carries it on, and hears of a
// kill with no board running.
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
	if err == nil || !strings.Contains(out, `extension "noop" refused the spawn: the spawn budget is spent`) {
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
	if _, err := os.Stat(filepath.Join(data, "form.txt")); err == nil {
		t.Fatal("a spawn from the CLI carried form values")
	}
	if _, err := os.Stat(filepath.Join(data, "formspawned.txt")); err == nil {
		t.Fatal("a spawn from the CLI was reported as one from the form")
	}

	// The policy counts the caller's live children on the board, from the
	// CLI process, before anything is launched: the seeded child is dead,
	// and the one just spawned is live.
	out, err = run("spawn", "--tool", "envecho", "--name", "one-too-many", "--directory", work)
	if err == nil || !strings.Contains(out, `extension "noop" refused the spawn: ca11e400 already has live children: within-budget`) {
		t.Fatalf("a spawn over the live-children cap: %v\n%s", err, out)
	}

	out, err = run("spawn", "--tool", "envecho", "--name", "detached", "--nest=false", "--prompt", "the sub-task", "--directory", work, "--json")
	if err != nil {
		t.Fatalf("spawn detached: %v\n%s", err, out)
	}
	detached := storedSession(t, filepath.Join(home, "state.db"), idOf(t, out))
	if detached.ParentID != "ca11e400" || !strings.Contains(detached.LaunchPrompt, "NOOP PREFIX for ca11e400\n\nthe sub-task\n\nNOOP SUFFIX for ca11e400") {
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
	if prompt := storedSession(t, filepath.Join(home, "state.db"), moved).LaunchPrompt; !strings.Contains(prompt, "NOOP PREFIX from 0de0c0de\n\n") {
		t.Fatalf("the migration launched on %q, want the extension's prefix", prompt)
	}

	// No board is running: the kill is heard in the process that made it,
	// before the command returns, and only once.
	if _, err := os.Stat(filepath.Join(data, "kills.txt")); err == nil {
		t.Fatal("a kill was reported before anything was killed")
	}
	if out, err := run("kill", spawned); err != nil {
		t.Fatalf("kill: %v\n%s", err, out)
	}
	body, err := os.ReadFile(filepath.Join(data, "kills.txt"))
	if err != nil {
		t.Fatalf("the kill command returned before the extension heard of it: %v", err)
	}
	if want := spawned + " cli ca11e400 dead\n"; string(body) != want {
		t.Fatalf("kills.txt = %q, want %q", body, want)
	}
	if out, err := run("kill", "ca11e400"); err == nil {
		t.Fatalf("a session killed itself:\n%s", out)
	}
	if body, _ := os.ReadFile(filepath.Join(data, "kills.txt")); string(body) != spawned+" cli ca11e400 dead\n" {
		t.Fatalf("a refused kill was reported: %q", body)
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

func queuedCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	counts, err := st.QueuedCounts()
	if err != nil {
		t.Fatal(err)
	}
	return counts
}

// A spawn policy whose section is refused fails closed in a build from
// outside this module: a spawn from the CLI or through the MCP tool is
// refused with the reason, while the other extensions' tools and launch
// hooks carry on, and a migration, which is not a spawn, still goes ahead.
func TestExternalBuildRefusesSpawnsWhileAPolicyIsDisabled(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("a migration launches a tmux pane")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("failclosed")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+envEchoTool+"\n[extensions.extra]\nbogus = 1\n")
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	env = append(env, "GATE_INBOX_SESSION_ID=ca11e400")
	seedSessions(t, filepath.Join(home, "state.db"))
	work := t.TempDir()
	seedOpenCodeSource(t, filepath.Join(home, "state.db"), work)
	data := filepath.Join(home, "extensions", "noop")
	const refused = `spawn refused: extension "extra" is disabled: [extensions.extra]: unknown key(s): bogus`

	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("spawn", "--tool", "envecho", "--name", "within-budget", "--directory", work)
	if err == nil || !strings.Contains(out, refused) {
		t.Fatalf("a CLI spawn with the policy disabled: %v\n%s", err, out)
	}

	session := connectFixture(t, bin, env)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_session", Arguments: map[string]any{
		"tool": "envecho", "name": "within-budget", "directory": work, "prompt": "work",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Content[0].(*mcp.TextContent).Text; !result.IsError || !strings.Contains(got, refused) {
		t.Fatalf("an MCP spawn with the policy disabled answered %q", got)
	}
	if got := callText(t, session, "noop_ping", map[string]any{}); !strings.HasSuffix(got, " from ca11e400") {
		t.Fatalf("noop_ping answered %q", got)
	}
	if got := callText(t, session, "items_draw", map[string]any{}); got != "item" {
		t.Fatalf("items_draw answered %q", got)
	}
	if _, err := os.Stat(filepath.Join(data, "spawned.txt")); err == nil {
		t.Fatal("a refused spawn was reported as launched")
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
}
