package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// promptTool draws a shell prompt and reads what is typed at it, so the
// board sees a session at rest that a message can be delivered to.
const promptTool = `
[tools.prompter]
command = "sh -c 'printf \"$ \"; cat >/dev/null; :' --"
default_status = "idle"
activity_cutoff = "(?m)^\\$"
`

// A line the operator sends from a shell with --as-human reaches an
// extension compiled outside this module, through the running board's
// OnOperator, once the board has typed it into the session.
func TestExternalBuildHearsAShellSendAsTheOperator(t *testing.T) {
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is needed to give the board a terminal")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("operator")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+promptTool)
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	data := filepath.Join(home, "extensions", "noop")
	work := t.TempDir()

	run := func(env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	// A session for the operator to answer, spawned by the seeded caller.
	agent := append(withoutKey(env, "GATE_INBOX_SESSION_ID"), "GATE_INBOX_SESSION_ID=ca11e400")
	target := idOf(t, run(agent, "spawn", "--tool", "prompter", "--name", "asks-the-operator", "--prompt", "wait for the operator", "--directory", work, "--json"))

	board := exec.Command(script, "-qec", bin, "/dev/null")
	if runtime.GOOS != "linux" {
		board = exec.Command(script, "-q", "/dev/null", bin)
	}
	board.Env = env
	var out strings.Builder
	board.Stdout, board.Stderr = &out, &out
	if err := board.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		board.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		board.Process.Kill()
		<-exited
	})
	waitForFile(t, filepath.Join(data, "started.txt"), "", exited, &out)

	// The operator's own shell: no session id, and not an agent's.
	operator := withoutKey(withoutKey(env, "GATE_INBOX_SESSION_ID"), "CLAUDECODE")
	run(operator, "send", target, "yes, ship it", "--as-human")
	waitForFile(t, filepath.Join(data, "operator.txt"), target+` cli false "yes, ship it"`, exited, &out)
	// Two more passes see the message delivered and must not report it
	// again.
	time.Sleep(5 * time.Second)
	heard, err := os.ReadFile(filepath.Join(data, "operator.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(heard), " cli ") != 1 {
		t.Fatalf("want the one shell send reported once as cli:\n%s", heard)
	}
}

func withoutKey(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if k, _, _ := strings.Cut(entry, "="); k != key {
			out = append(out, entry)
		}
	}
	return out
}
