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
	terminal := tmuxtest.Socket(t, "attachterm")
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

// An adopted pane in the manager's own session is the self-attach case even
// though the pane is not the manager's. It resolves to the manager's session
// name, so the nested client starts in the session the manager is drawing and
// lands on its current window -- usually the manager's own screen, rendered
// recursively and unreadable. $TMUX has to stay on for that target so tmux
// refuses it.
//
// The bug this covers: the inside check compared the target to the manager's own
// pane, so a sibling pane of the manager's own session sailed through and the
// board mirrored itself.
func TestAttachRefusesAnAdoptedSiblingOfTheManagersSession(t *testing.T) {
	driver := requireTmux(t)
	socket := operatorServer(t)

	out, err := tmuxOn(socket, "list-panes", "-t", operatorSession+":1", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes window 1: %v: %s", err, out)
	}
	adoptedPane := strings.TrimSpace(string(out))
	out, err = tmuxOn(socket, "list-panes", "-t", operatorSession+":0", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes window 0: %v: %s", err, out)
	}
	managerPane := strings.TrimSpace(string(out))

	id := uniqueID("sibling")
	if err := driver.Adopt(id, Target{Socket: socket, Name: adoptedPane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	// The manager runs in window 0 of the same session the adopted pane is in.
	t.Setenv("TMUX", tmuxtest.SocketPath(socket)+",1,0")
	t.Setenv("TMUX_PANE", managerPane)
	cmd := driver.AttachCommand(id)
	if cmd.Env != nil && !hasTmuxEnv(cmd.Env) {
		t.Error("dropped $TMUX for an adopted pane in the manager's own session; " +
			"tmux nests the attach and the board mirrors itself")
	}
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
	socket := tmuxtest.Socket(t, "detached")
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
	terminal := tmuxtest.Socket(t, "soloterm")
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

// A manager running in a pane of the target's own server must not start a
// second client there: that client's terminal is one of the server's panes,
// and the server deadlocked writing into a pty only it could drain. The attach
// moves the operator's existing client instead, and still blocks until the
// operator is back in the manager's session, which is what the caller's
// release-and-restore of the screen depends on.
func TestAttachOnTheManagersServerSwitchesTheOperatorsClient(t *testing.T) {
	driver := requireTmux(t)
	socket := operatorServer(t)
	if out, err := tmuxOn(socket, "new-session", "-d", "-s", "other", "-x", "80", "-y", "24", "cat").CombinedOutput(); err != nil {
		t.Fatalf("new-session other: %v: %s", err, out)
	}
	out, err := tmuxOn(socket, "list-panes", "-t", "=other:0", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes other: %v: %s", err, out)
	}
	targetPane := strings.TrimSpace(string(out))
	out, err = tmuxOn(socket, "list-panes", "-t", operatorSession+":0", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes manager: %v: %s", err, out)
	}
	managerPane := strings.TrimSpace(string(out))

	id := uniqueID("switch")
	if err := driver.Adopt(id, Target{Socket: socket, Name: targetPane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	t.Setenv("TMUX", tmuxtest.SocketPath(socket)+",1,0")
	t.Setenv("TMUX_PANE", managerPane)

	cmd := driver.AttachCommand(id)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start attach: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { cmd.Process.Kill() })

	clients := func() string {
		out, _ := tmuxOn(socket, "list-clients", "-F", "#{session_name}").CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	deadline := time.Now().Add(5 * time.Second)
	for clients() != "other" {
		if time.Now().After(deadline) {
			t.Fatalf("the operator's one client never reached the target; clients: %q", clients())
		}
		time.Sleep(50 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("the attach returned while the operator was still away: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	tty, err := tmuxOn(socket, "list-clients", "-F", "#{client_tty}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-clients: %v: %s", err, tty)
	}
	if out, err := tmuxOn(socket, "switch-client", "-c", strings.TrimSpace(string(tty)), "-t", "="+operatorSession).CombinedOutput(); err != nil {
		t.Fatalf("switch back: %v: %s", err, out)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("attach exited with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the attach never returned after the operator came back")
	}
	hooks, _ := tmuxOn(socket, "show-hooks", "-t", "="+operatorSession+":").CombinedOutput()
	if strings.Contains(string(hooks), returnHook) {
		t.Errorf("the return hooks outlived the attach:\n%s", hooks)
	}
}
