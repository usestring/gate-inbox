package app

import (
	"encoding/json"
	"fmt"
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
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+promptTool+"\n[extensions.tally]\nanswers = \"asks-the-operator\"\n")
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
	var sent struct {
		MessageID int64 `json:"message_id"`
		Handled   []struct {
			Result string `json:"result"`
		} `json:"handled"`
	}
	reply := run(operator, "send", target, "yes, ship it", "--as-human", "--json")
	if err := json.Unmarshal([]byte(reply), &sent); err != nil || sent.MessageID == 0 ||
		len(sent.Handled) != 1 || sent.Handled[0].Result != "answered" {
		t.Fatalf("send --json = %s (%v)", reply, err)
	}
	// The send and its delivery name the same message, which is what an
	// extension that handled the one dedupes the other on.
	id := fmt.Sprintf(" id=%d", sent.MessageID)
	told, err := os.ReadFile(filepath.Join(home, "extensions", "tally", "sent.txt"))
	if err != nil || string(told) != target+id+"\n" {
		t.Fatalf("tally was told %q (%v), want %q", told, err, target+id)
	}
	waitForFile(t, filepath.Join(data, "operator.txt"), target+` cli false "yes, ship it"`+id, exited, &out)
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

// An extension compiled outside this module hears the operator's shell
// send in the sending process, and what it makes of it comes back to the
// sender with no board running: in the text, and under "handled" in the
// JSON. A send to a session it does not answer for comes back plain.
func TestExternalBuildAnswersTheOperatorsSendWithNoBoard(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("sessions run in a tmux server")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("handled")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+promptTool+"\n[extensions.tally]\nanswers = \"asks-the-operator\"\n")
	home := envValue(env, "GATE_INBOX_HOME")
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
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
	agent := append(withoutKey(env, "GATE_INBOX_SESSION_ID"), "GATE_INBOX_SESSION_ID=ca11e400")
	asks := idOf(t, run(agent, "spawn", "--tool", "prompter", "--name", "asks-the-operator", "--prompt", "wait", "--directory", work, "--json"))
	other := idOf(t, run(agent, "spawn", "--tool", "prompter", "--name", "just-working", "--prompt", "work", "--directory", work, "--json"))

	operator := withoutKey(withoutKey(env, "GATE_INBOX_SESSION_ID"), "CLAUDECODE")
	var sent struct {
		MessageID int64 `json:"message_id"`
		Handled   []struct {
			Extension string `json:"extension"`
			Result    string `json:"result"`
		} `json:"handled"`
	}
	out := run(operator, "send", asks, "yes, ship it", "--as-human", "--json")
	if err := json.Unmarshal([]byte(out), &sent); err != nil {
		t.Fatalf("send --json: %v\n%s", err, out)
	}
	if sent.MessageID == 0 || len(sent.Handled) != 1 || sent.Handled[0].Extension != "tally" || sent.Handled[0].Result != "answered" {
		t.Fatalf("send to the session tally answers for = %s", out)
	}
	if out := run(operator, "send", asks, "and the docs", "--as-human"); !strings.Contains(out, "Extension tally: answered") {
		t.Fatalf("the text does not say what tally made of it:\n%s", out)
	}

	out = run(operator, "send", other, "carry on", "--as-human", "--json")
	if strings.Contains(out, "handled") {
		t.Fatalf("a send to another session came back handled:\n%s", out)
	}
	if out := run(operator, "send", other, "and the tests", "--as-human"); strings.Contains(out, "tally") {
		t.Fatalf("a send to another session mentions tally:\n%s", out)
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
