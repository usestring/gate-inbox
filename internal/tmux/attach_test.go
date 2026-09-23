package tmux

import (
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The second half of the same mistake, in a different call site.
//
// The manager's own control clients were fixed in sample-repo#897/#900 to attach to
// a session name rather than to an adopted pane, because attach-session given
// a pane id also makes that pane's window the session's current window, and
// every client of a session shows that window. They no longer attach to a
// pane's session at all -- startControl refuses any session outside the
// manager's gi_ prefix -- but AttachCommand is the one attach that has to
// land in somebody else's session, since that is what the operator pressed a
// key for. It kept passing TargetName(id), which for an adopted pane is the
// pane. So pressing enter on an adopted row walked the operator's own
// terminal to another window.
//
// The assertion is on the operator's window index, not on the argument: an
// argument test can be satisfied by a different wrong argument.
func TestAttachDoesNotMoveTheOperatorsWindow(t *testing.T) {
	driver := requireTmux(t)
	socket := operatorServer(t)
	out, err := tmuxOn(socket, "list-panes", "-t", operatorSession+":1", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes: %v: %s", err, out)
	}
	pane := strings.TrimSpace(string(out))
	if got := currentWindow(t, socket); got != "0" {
		t.Fatalf("the operator's session starts on window %s, want 0", got)
	}

	id := uniqueID("attach")
	if err := driver.Adopt(id, Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	// A real terminal for the attach to land in: a nested tmux is the only
	// way a test can hand a command a pty.
	terminal := tmuxtest.NewSocket("attachterm")
	tmuxOn(terminal, "kill-server").Run()
	t.Cleanup(func() { tmuxOn(terminal, "kill-server").Run() })
	if out, err := tmuxOn(terminal, "new-session", "-d", "-s", "term", "-x", "80", "-y", "24",
		shellLine(driver.AttachCommand(id).Args)).CombinedOutput(); err != nil {
		t.Fatalf("attach terminal: %v: %s", err, out)
	}
	waitForClients(t, socket, 2)

	if got := currentWindow(t, socket); got != "0" {
		t.Fatalf("attaching to an adopted pane moved the operator's view to window %s; "+
			"every client of a session shows the session's current window", got)
	}
}

// The audit behind this test, so the next reader does not have to redo it.
// Of every tmux command this package aims a -t at, exactly one changes what a
// session's clients are looking at when handed a pane id: attach-session.
// switch-client, select-window and select-pane would too, and the package
// issues none except the guarded select inside AttachCommand. The rest --
// capture-pane, display-message -p, send-keys, paste-buffer, kill-pane,
// list-panes, resize-window, set-window-option -- resolve a pane without
// selecting it, and set-option is session-scoped and only ever handed a
// managed session name. So attach-session is the whole class, and it has two
// call sites: startControl and AttachCommand.
//
// The class, not the instance. Every attach-session this package builds names
// an exact session with "=", which is what stops a pane id from ever
// resolving to its session again: tmux answers "can't find session: %1"
// rather than attaching and moving a window.
//
// sample-repo#900 fixed one call site when it should have fixed the class, which is
// why AttachCommand survived it. This is the test that would have caught it.
func TestEveryAttachNamesAnExactSession(t *testing.T) {
	driver := requireTmux(t)
	socket := operatorServer(t)
	out, err := tmuxOn(socket, "list-panes", "-t", operatorSession+":1", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes: %v: %s", err, out)
	}
	pane := strings.TrimSpace(string(out))

	adopted := uniqueID("cls")
	if err := driver.Adopt(adopted, Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	managed := uniqueID("clsmanaged")
	if err := driver.Create(managed, "/tmp", "", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(managed) })

	seen := 0
	for _, id := range []string{adopted, managed} {
		for _, arg := range attachTargetsOf(driver.AttachCommand(id).Args) {
			seen++
			if !strings.HasPrefix(arg, "=") {
				t.Errorf("%s: attach target %q is not an exact session name; a pane id here "+
					"resolves to its session and moves that session's window", id, arg)
			}
			if strings.HasPrefix(strings.TrimPrefix(arg, "="), "%") {
				t.Errorf("%s: attach aimed at a pane id: %q", id, arg)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("found %d attach targets across two sessions, want 2", seen)
	}

	// The "=" is load-bearing rather than decorative: tmux must refuse a pane
	// id outright instead of resolving it.
	if out, err := tmuxOn(socket, "attach-session", "-t", "="+pane).CombinedOutput(); err == nil ||
		!strings.Contains(string(out), "can't find session") {
		t.Fatalf(`attach -t "=%s" did not refuse: %v: %s`, pane, err, out)
	}
}

// shellLine turns an argv into one shell command. Every argument is quoted:
// the attach carries a tmux command separator ";" that a shell would
// otherwise read as its own, which silently drops the attach and leaves the
// test asserting against a command that never ran.
func shellLine(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = ShellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

// attachTargetsOf pulls the -t of every attach-session in one tmux argv.
// AttachCommand may put an if-shell ahead of the attach, and that carries a
// -t of its own, so the scan keys on the attach-session verb rather than on
// the first -t it finds.
func attachTargetsOf(argv []string) []string {
	var targets []string
	for i, arg := range argv {
		if arg != "attach-session" {
			continue
		}
		for j := i + 1; j+1 < len(argv)+1 && j < len(argv); j++ {
			if argv[j] == ";" {
				break
			}
			if argv[j] == "-t" && j+1 < len(argv) {
				targets = append(targets, argv[j+1])
				break
			}
		}
	}
	return targets
}

// The operator gets the pane they picked when nobody else is looking at the
// session. The guard is if-shell inside tmux rather than a read this process
// acts on, so the check and the select cannot straddle another client's
// attach.
func TestAttachLandsOnTheAdoptedPaneWhenNobodyIsWatching(t *testing.T) {
	driver := requireTmux(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := tmuxtest.NewSocket("detached")
	tmuxOn(socket, "kill-server").Run()
	t.Cleanup(func() { tmuxOn(socket, "kill-server").Run() })
	for _, args := range [][]string{
		{"new-session", "-d", "-s", "solo", "-x", "80", "-y", "24", "cat"},
		{"new-window", "-t", "solo", "cat"},
		{"select-window", "-t", "solo:0"},
	} {
		if out, err := tmuxOn(socket, args...).CombinedOutput(); err != nil {
			t.Fatalf("setup %v: %v: %s", args, err, out)
		}
	}
	out, err := tmuxOn(socket, "list-panes", "-t", "solo:1", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes: %v: %s", err, out)
	}
	pane := strings.TrimSpace(string(out))

	id := uniqueID("solo")
	if err := driver.Adopt(id, Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	terminal := tmuxtest.NewSocket("soloterm")
	tmuxOn(terminal, "kill-server").Run()
	t.Cleanup(func() { tmuxOn(terminal, "kill-server").Run() })
	if out, err := tmuxOn(terminal, "new-session", "-d", "-s", "term", "-x", "80", "-y", "24",
		shellLine(driver.AttachCommand(id).Args)).CombinedOutput(); err != nil {
		t.Fatalf("attach terminal: %v: %s", err, out)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := tmuxOn(socket, "display-message", "-p", "-t", "solo", "#{window_index}").CombinedOutput()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the attach never landed on the adopted pane's window: %s", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
