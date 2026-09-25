package adopt

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// paneServer starts a private tmux server running command, and returns its
// socket. Private because a scan reads whatever server it is pointed at, and a
// test must never be pointed at somebody's real one.
func paneServer(t *testing.T, dir, command string) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// A name in a family tmuxtest knows: the old one was outside every
	// prefix the reaper matches, so nothing this package left behind was
	// ever collected by anything but a human.
	socket := tmuxtest.NewSocket("adopt")
	out, err := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", "work",
		"-c", dir, "-x", "80", "-y", "24", command).CombinedOutput()
	if err != nil {
		t.Fatalf("start pane: %v: %s", err, out)
	}
	// Killing the panes left the server up whenever a pane had already gone
	// or a kill lost its race, and a tmux server holds its socket for as long
	// as it lives. kill-server is the one call that ends it either way.
	t.Cleanup(func() {
		tmuxtest.KillServer(socket)
		tmuxtest.ReapSocket(socket)
	})
	return socket
}

// waitForPane gives the command in the pane time to draw before a scan reads it.
func waitForPane(t *testing.T, socket, want string) []Candidate {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		panes := Panes(socket)
		if len(panes) > 0 {
			if text, err := Capture(socket, panes[0].PaneID); err == nil && strings.Contains(text, want) {
				return panes
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never drew %q", want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// fakeAgent writes a script that behaves the way an agent CLI does from the
// outside: it draws a prompt marker and then waits. Named, so the process tree
// carries the name the way a real CLI would.
func fakeAgent(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	// Deliberately does not exec: a wrapper that execs away loses its own name
	// from the process tree, while a real agent CLI keeps its.
	script := "#!/bin/sh\nprintf '❯ '\nwhile IFS= read -r line; do :; done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func promptTool(name, command string) Tool {
	return Tool{Name: name, Command: command, Prompt: regexp.MustCompile(`(?m)^\x{276f}`)}
}

func TestPanesReadsARealPane(t *testing.T) {
	dir := t.TempDir()
	socket := paneServer(t, dir, fakeAgent(t, "someagent"))
	panes := waitForPane(t, socket, "❯")

	if len(panes) != 1 {
		t.Fatalf("got %d panes, want 1", len(panes))
	}
	pane := panes[0]
	if !strings.HasPrefix(pane.PaneID, "%") {
		t.Errorf("pane id = %q, want a tmux %%id", pane.PaneID)
	}
	if pane.PID <= 0 {
		t.Errorf("pane pid = %d", pane.PID)
	}
	if pane.Session != "work" {
		t.Errorf("session = %q, want work", pane.Session)
	}
	if pane.Socket != socket {
		t.Errorf("socket = %q, want %q", pane.Socket, socket)
	}
}

// The whole point of adoption: a pane the manager never launched is identified
// from the outside, by what is running in it and what it is drawing.
func TestIdentifyAdoptsARealAgentPaneOnBothSignals(t *testing.T) {
	dir := t.TempDir()
	socket := paneServer(t, dir, fakeAgent(t, "someagent"))
	panes := waitForPane(t, socket, "❯")

	pane, err := Capture(socket, panes[0].PaneID)
	if err != nil {
		t.Fatal(err)
	}
	match, ok := Identify(panes[0], []Tool{promptTool("someagent", "someagent --resume")}, pane, NewProcTable())
	if !ok {
		t.Fatal("a real agent pane was not identified at all")
	}
	if !match.Confident() {
		t.Fatalf("match is not confident: signals = %v", match.Signals)
	}
}

// A pane running the tool but showing no prompt is an agent: an agent
// mid-turn, holding a dialog open, or scrolled back has no marker on screen
// and is exactly the session worth surfacing. A pane showing the prompt with
// no such process is a guess, and the guess has been wrong on a real board.
func TestTheProcessDecidesAndThePromptAloneDoesNot(t *testing.T) {
	dir := t.TempDir()
	socket := paneServer(t, dir, fakeAgent(t, "someagent"))
	panes := waitForPane(t, socket, "❯")
	pane, err := Capture(socket, panes[0].PaneID)
	if err != nil {
		t.Fatal(err)
	}

	// The process is there; the prompt belongs to a tool that is not running.
	commandOnly, ok := Identify(panes[0], []Tool{{
		Name: "someagent", Command: "someagent", Prompt: regexp.MustCompile(`^never-drawn$`),
	}}, pane, NewProcTable())
	if !ok || !commandOnly.Confident() {
		t.Errorf("a pane whose process IS the tool was refused: ok=%v signals=%v", ok, commandOnly.Signals)
	}

	// The prompt is there; the tool named is a different program.
	promptOnly, ok := Identify(panes[0], []Tool{promptTool("otheragent", "otheragent")}, pane, NewProcTable())
	if !ok || promptOnly.Confident() {
		t.Errorf("prompt match alone read as confident: ok=%v signals=%v", ok, promptOnly.Signals)
	}
}

// A sentence typed at a shell is a command, so a shell block is never a
// candidate however much it looks like one.
func TestAShellBlockIsNeverAdopted(t *testing.T) {
	dir := t.TempDir()
	socket := paneServer(t, dir, fakeAgent(t, "someagent"))
	panes := waitForPane(t, socket, "❯")
	pane, err := Capture(socket, panes[0].PaneID)
	if err != nil {
		t.Fatal(err)
	}

	shell := promptTool("someagent", "someagent")
	shell.Shell = true
	if _, ok := Identify(panes[0], []Tool{shell}, pane, NewProcTable()); ok {
		t.Error("a shell block was offered for adoption")
	}
}

// Tools share prompt markers, so the one corroborated by the process tree wins
// rather than whichever config block came first.
func TestBothSignalsBeatOne(t *testing.T) {
	dir := t.TempDir()
	socket := paneServer(t, dir, fakeAgent(t, "someagent"))
	panes := waitForPane(t, socket, "❯")
	pane, err := Capture(socket, panes[0].PaneID)
	if err != nil {
		t.Fatal(err)
	}

	tools := []Tool{
		promptTool("lookalike", "lookalike"),
		promptTool("someagent", "someagent"),
	}
	match, ok := Identify(panes[0], tools, pane, NewProcTable())
	if !ok {
		t.Fatal("nothing identified")
	}
	if match.Tool != "someagent" {
		t.Errorf("tool = %q, want someagent", match.Tool)
	}
}

// Now that one signal can identify a tool, two tools matching one signal each
// have to be separated by which signal it was. Both orderings are asserted
// because the caller iterates a map: an answer that depended on config order
// would be a coin flip on every scan rather than a stable wrong answer.
func TestTheProcessBeatsAPromptFromAnotherTool(t *testing.T) {
	dir := t.TempDir()
	socket := paneServer(t, dir, fakeAgent(t, "someagent"))
	panes := waitForPane(t, socket, "❯")
	pane, err := Capture(socket, panes[0].PaneID)
	if err != nil {
		t.Fatal(err)
	}

	// "lookalike" draws the marker on screen and is not running; "someagent"
	// is running and its configured marker is never drawn.
	running := Tool{Name: "someagent", Command: "someagent", Prompt: regexp.MustCompile(`^never-drawn$`)}
	drawing := promptTool("lookalike", "lookalike")

	for _, tools := range [][]Tool{{drawing, running}, {running, drawing}} {
		match, ok := Identify(panes[0], tools, pane, NewProcTable())
		if !ok {
			t.Fatalf("%s: nothing identified", toolOrder(tools))
		}
		if match.Tool != "someagent" {
			t.Errorf("%s: tool = %q, want someagent (the one actually running)", toolOrder(tools), match.Tool)
		}
		if !match.Confident() {
			t.Errorf("%s: match not confident: signals=%v", toolOrder(tools), match.Signals)
		}
	}
}

func toolOrder(tools []Tool) string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return "order " + strings.Join(names, ",")
}

// A scan runs on a timer against sockets that come and go, so a server that is
// not there is an empty answer rather than a failure.
func TestScanningAServerThatIsNotThereIsEmpty(t *testing.T) {
	if panes := Panes("adopt-test-no-such-server"); len(panes) != 0 {
		t.Errorf("got %d panes from a dead socket", len(panes))
	}
	if _, err := Capture("adopt-test-no-such-server", "%0"); err == nil {
		t.Error("capturing from a dead socket did not fail")
	}
}

// waitForExec blocks until a started child has actually replaced its image.
// Start returns as soon as the clone lands, and until execve runs the child
// carries a copy of this process's memory, so /proc/<pid>/cmdline answers
// empty -- which Cmdlines drops, leaving a descendant that PIDs can see and
// Cmdlines cannot. It is a narrow window and it does open: reading a fresh
// child's cmdline three thousand times on a development host caught it 378 times.
func waitForExec(t *testing.T, pid int, want string) {
	t.Helper()
	if _, err := os.Stat("/proc/self"); err != nil {
		// No /proc to read: this platform cannot show the window either.
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
		if err == nil && strings.Contains(string(raw), want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("child %d never exec'd %q", pid, want)
}

// TestProcTableFindsADescendant pins the ancestry the command signal rests on.
// An agent is normally a child of the pane's own process rather than the
// process tmux reports, so a table that could not see below the root would
// leave every scan unconfident and adopt nothing.
func TestProcTableFindsADescendant(t *testing.T) {
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	waitForExec(t, child.Process.Pid, "sleep")

	table := NewProcTable()
	self := int32(os.Getpid())

	var found bool
	for _, pid := range table.PIDs(self) {
		if pid == child.Process.Pid {
			found = true
		}
	}
	if !found {
		t.Errorf("PIDs(%d) did not include child %d: %v", self, child.Process.Pid, table.PIDs(self))
	}

	found = false
	for _, line := range table.Cmdlines(self) {
		if strings.Contains(line, "sleep") {
			found = true
		}
	}
	if !found {
		t.Errorf("Cmdlines(%d) named no descendant running sleep: %v", self, table.Cmdlines(self))
	}
}

// A pane is identified by the program its processes run, not by an argument
// that happens to end in the tool's name. The short tool names are the ones
// that bite -- "pi" is a directory component on any Raspberry Pi box, and the
// command signal alone is enough to adopt since #997, so a match here hands
// the manager a plain shell it will eventually type into.
func TestAPathArgumentDoesNotNameTheProgram(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want bool
	}{
		{"a path argument that ends in the tool name", []string{"ls /home/pi"}, false},
		{"the tool name deeper in a path argument", []string{"cat /home/pi/notes.txt"}, false},
		{"a shell running such a command", []string{"sh -c ls /home/pi"}, false},
		{"an editor opening a file under it", []string{"vim -p /srv/pi"}, false},
		{"the program itself", []string{"pi --session-id abc"}, true},
		{"the program by absolute path", []string{"/usr/local/bin/pi"}, true},
		{"a shebang script, the interpreter first", []string{"/bin/sh /usr/local/bin/pi"}, true},
		{"an env shebang", []string{"env node /opt/tools/pi"}, true},
		{"nothing running it at all", []string{"-zsh", "ls"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := namesProgram(tc.argv, "pi"); got != tc.want {
				t.Errorf("namesProgram(%q, \"pi\") = %v, want %v", tc.argv, got, tc.want)
			}
		})
	}
}
