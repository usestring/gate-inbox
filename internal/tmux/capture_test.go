package tmux

import (
	"errors"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// countingTmux puts a logging shim in front of the real tmux binary, so a
// test can count the processes a pass forks rather than assert about them.
func countingTmux(t *testing.T, driver *Driver) func() []string {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	shim := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\n{ printf '%s\\n' \"$*\"; } >> " + log + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatalf("shim: %v", err)
	}
	driver.bin = shim
	return func() []string {
		raw, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		var calls []string
		for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			if line != "" {
				calls = append(calls, line)
			}
		}
		return calls
	}
}

// The pass that costs the whole program: every live pane read at once. It has
// to reach panes on a server the manager does not own -- which is the entire
// board for an operator who adopts their own sessions -- and it has to do it
// in one forked tmux per server rather than one per pane, which is the 17x
// this exists to remove.
func TestCapturePanesReadsEveryServerWithOneForkEach(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	managed := uniqueID("mine")
	if err := driver.Create(managed, "/tmp", "printf 'managed-marker'; cat", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(managed) })
	ids := []string{managed}
	for i, pane := range panes {
		id := uniqueID("borrowed" + strconv.Itoa(i))
		adopt(t, driver, id, socket, pane)
		if err := driver.SendText(id, "adopted-marker-"+strconv.Itoa(i)); err != nil {
			t.Fatalf("SendText: %v", err)
		}
		waitForPane(t, socket, pane, "adopted-marker-"+strconv.Itoa(i))
		ids = append(ids, id)
	}
	waitForPane(t, driver.socket, sessionName(managed), "managed-marker")

	calls := countingTmux(t, driver)
	t.Cleanup(driver.CloseCaptureClients)
	// The manager's own socket and the foreign one: a fork each is the floor
	// this asserts, and it does not move with the number of panes.
	const servers = 2
	for pass := 0; pass < 3; pass++ {
		before := len(calls())
		got := driver.CapturePanes(ids)
		for i, id := range ids {
			capture, listed := got[id]
			if !listed || capture.Err != nil {
				t.Fatalf("pass %d: %s: %v", pass, id, capture.Err)
			}
			want := "adopted-marker-" + strconv.Itoa(i-1)
			if i == 0 {
				want = "managed-marker"
			}
			if !strings.Contains(capture.Text, want) {
				t.Fatalf("pass %d: %s read the wrong pane: %q", pass, id, capture.Text)
			}
		}
		forked := calls()[before:]
		captures := 0
		for _, call := range forked {
			if strings.Contains(call, "capture-pane") {
				captures++
			}
		}
		// One forked tmux per server, every pass, with that server's panes
		// chained inside it. What matters is that it is a fork at all:
		// reading a pane over the pooled client would cost nothing here and
		// leak a reply's worth of the server per capture (tmux/tmux#5553),
		// so paying a process is the point. Paying one per pane was not --
		// the process is the whole cost, and the panes ride it for free.
		if captures != servers {
			t.Fatalf("pass %d forked %d captures for %d servers holding %d panes: %q",
				pass, captures, servers, len(ids), forked)
		}
		// Nothing beyond the captures themselves: reading a board must not
		// stand up a control client, whose replies are what the server
		// would then hold on to.
		if len(forked) != captures {
			t.Fatalf("pass %d forked %d processes for %d captures: %q", pass, len(forked), captures, forked)
		}
	}
}

// A server that cannot be reached has to come back as a failure and never as
// an empty pane: empty text reads as an agent that stopped printing, and the
// poll would write a status off it.
func TestCapturePanesReportsAnUnreachableServer(t *testing.T) {
	driver := requireTmux(t)
	id := uniqueID("stranded")
	adopt(t, driver, id, tmuxtest.NewSocket("gone"), "%0")
	t.Cleanup(driver.CloseCaptureClients)

	capture, listed := driver.CapturePanes([]string{id})[id]
	if !listed {
		t.Fatal("a session that could not be read must still be reported")
	}
	if capture.Err == nil {
		t.Fatalf("an unreachable server answered with %q and no error", capture.Text)
	}
	if capture.Text != "" {
		t.Fatalf("an unreachable server produced pane text: %q", capture.Text)
	}
}

// Nothing is opened for an empty board, so a manager with no sessions holds
// no clients on anybody's server.
func TestCapturePanesOpensNothingForNoSessions(t *testing.T) {
	driver := requireTmux(t)
	calls := countingTmux(t, driver)
	if got := driver.CapturePanes(nil); len(got) != 0 {
		t.Fatalf("captures for no sessions: %+v", got)
	}
	if forked := calls(); len(forked) != 0 {
		t.Fatalf("an empty board forked %q", forked)
	}
}

// The failure model in one test: one deadline covers the whole list, so a
// server that stops answering costs a single wait rather than one per pane.
// Eighty-seven panes at commandTimeout each would be three minutes.
func TestBatchSpendsOneDeadlineOnTheWholeList(t *testing.T) {
	server := newFakeServer()
	server.send("%begin 1 0 0", "%end 1 0 0")

	type outcome struct {
		replies []Reply
		fatal   error
		took    time.Duration
	}
	done := make(chan outcome, 1)
	go func() {
		start := time.Now()
		replies, fatal := server.control.Batch([]string{"one", "two", "three"}, 250*time.Millisecond)
		done <- outcome{replies, fatal, time.Since(start)}
	}()
	waitWritten(t, server, "three\n")
	server.send("%begin 2 1 0", "first reply", "%end 2 1 0")

	select {
	case got := <-done:
		if !errors.Is(got.fatal, ErrControlTimedOut) {
			t.Fatalf("fatal = %v, want %v", got.fatal, ErrControlTimedOut)
		}
		if got.took > time.Second {
			t.Fatalf("the batch waited %v, which is one deadline per command", got.took)
		}
		if got.replies[0].Err != nil || got.replies[0].Text != "first reply" {
			t.Fatalf("the reply that did arrive was lost: %+v", got.replies[0])
		}
		for i := 1; i < 3; i++ {
			if !errors.Is(got.replies[i].Err, ErrControlTimedOut) {
				t.Fatalf("reply %d = %+v, want a timeout", i, got.replies[i])
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the batch never returned")
	}
}

// A command that fails on the server is that command's failure and no reason
// to throw the client away, or the first closed pane on a busy board would
// cost every other pane its capture.
func TestBatchKeepsAFailedCommandToItself(t *testing.T) {
	server := newFakeServer()
	server.send("%begin 1 0 0", "%end 1 0 0")

	type outcome struct {
		replies []Reply
		fatal   error
	}
	done := make(chan outcome, 1)
	go func() {
		replies, fatal := server.control.Batch([]string{"one", "two"}, 2*time.Second)
		done <- outcome{replies, fatal}
	}()
	waitWritten(t, server, "two\n")
	server.send("%begin 2 1 0", "can't find pane %99", "%error 2 1 0")
	server.send("%begin 3 2 0", "pane text", "%end 3 2 0")

	select {
	case got := <-done:
		if got.fatal != nil {
			t.Fatalf("a failed command must not fail the pipe: %v", got.fatal)
		}
		if got.replies[0].Err == nil {
			t.Fatal("the failing command reported no error")
		}
		if got.replies[1].Err != nil || got.replies[1].Text != "pane text" {
			t.Fatalf("the second command lost its reply: %+v", got.replies[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the batch never returned")
	}
}

// What replaced the old refusal. Control mode used to be denied on a pane the
// manager did not create, because a client that carries a size would drag the
// window of a session whose window-size is "latest" down to its own. The
// client now attaches with ignore-size, and this is the proof: a person is
// watching the pane through a real terminal, and their window keeps the size
// it had.
func TestControlClientLeavesAWatchedWindowAlone(t *testing.T) {
	driver := requireTmux(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := tmuxtest.NewSocket("watched")
	tmuxOn(socket, "kill-server").Run()
	if out, err := tmuxOn(socket, "new-session", "-d", "-s", "user", "-x", "200", "-y", "50", "cat").CombinedOutput(); err != nil {
		t.Fatalf("watched new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { tmuxOn(socket, "kill-server").Run() })
	if out, err := tmuxOn(socket, "set-window-option", "-t", "user", "window-size", "latest").CombinedOutput(); err != nil {
		t.Fatalf("window-size latest: %v: %s", err, out)
	}
	// A real terminal on the pane, at a size of its own: a nested tmux is
	// the one way to give a test a client with a pty behind it.
	watcher := tmuxtest.NewSocket("watcher")
	tmuxOn(watcher, "kill-server").Run()
	if out, err := tmuxOn(watcher, "new-session", "-d", "-s", "term", "-x", "132", "-y", "43",
		"tmux -L "+socket+" attach -t user").CombinedOutput(); err != nil {
		t.Fatalf("watcher new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { tmuxOn(watcher, "kill-server").Run() })

	windowSize := func() string {
		out, err := tmuxOn(socket, "display-message", "-p", "-t", "user", "#{window_width}x#{window_height}").CombinedOutput()
		if err != nil {
			t.Fatalf("window size: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	deadline := time.Now().Add(10 * time.Second)
	for strings.HasPrefix(windowSize(), "200x") {
		if time.Now().After(deadline) {
			t.Fatalf("the watching terminal never took the window, size = %s", windowSize())
		}
		time.Sleep(20 * time.Millisecond)
	}
	watched := windowSize()

	out, err := tmuxOn(socket, "list-panes", "-t", "user", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("watched list-panes: %v: %s", err, out)
	}
	pane := strings.TrimSpace(string(out))
	id := uniqueID("watched")
	adopt(t, driver, id, socket, pane)

	// The mirror that used to attach here is gone, so the pane is read the
	// way every adopted pane is read: the pooled poll client, which attaches
	// to the manager's own anchor session on this server and never joins the
	// watched one.
	t.Cleanup(driver.CloseCaptureClients)
	if got := driver.CapturePanes([]string{id})[id]; got.Err != nil {
		t.Fatalf("capture of the watched pane: %v", got.Err)
	}
	if got := windowSize(); got != watched {
		t.Fatalf("reading a watched pane resized the window: %s -> %s", watched, got)
	}
	// The size held, and it held for the right reason: nothing of ours is in
	// the watched session at all. ignore-size is why a client there would be
	// harmless; not being there is why the question does not arise.
	for _, client := range clientSessions(t, socket) {
		if client != "user" && client != anchorSession {
			t.Fatalf("the manager attached a client to %q", client)
		}
		if client == "user" && controlClientsOn(t, socket, "user") != 0 {
			t.Fatalf("a control client is attached to the session somebody is watching")
		}
	}
}

// clientSessions is the session each client of a server is attached to.
func clientSessions(t *testing.T, socket string) []string {
	t.Helper()
	out, err := tmuxOn(socket, "list-clients", "-F", "#{client_session}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-clients: %v: %s", err, out)
	}
	return strings.Fields(string(out))
}

// controlClientsOn counts the control-mode clients of one session.
func controlClientsOn(t *testing.T, socket, session string) int {
	t.Helper()
	out, err := tmuxOn(socket, "list-clients", "-t", session, "-F", "#{client_control_mode}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-clients on %s: %v: %s", session, err, out)
	}
	count := 0
	for _, mode := range strings.Fields(string(out)) {
		if mode == "1" {
			count++
		}
	}
	return count
}

// The outage in one assertion, aimed at the path that replaced the one that
// caused it.
//
// Reading and typing into a focused pane used to mean a control client
// attached to the session that held the pane. For an adopted pane that is a
// session the operator is working in, and one client per focus change reached
// 584 on their server and killed it. Both operations now ride the pooled
// pipe, whose client is attached to the manager's own anchor -- so however
// hard a caller drives them, the operator's session must never acquire a
// client.
//
// The assertion is on list-clients rather than on which function was called,
// because the count on the server is the thing that ran out.
func TestReadingAndTypingIntoAnAdoptedPaneAttachesOnlyToTheAnchor(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("pipeonly")
	adopt(t, driver, id, socket, panes[1])
	t.Cleanup(driver.CloseCaptureClients)

	// Many times over, because the bug was not one attach: it was one per
	// focus change on a board somebody was scrolling through.
	for i := 0; i < 20; i++ {
		if _, err := driver.CapturePane(id); err != nil {
			t.Fatalf("CapturePane %d: %v", i, err)
		}
		if !driver.PipeSend(id, "send-keys -t "+driver.TargetName(id)+" x") {
			t.Fatalf("PipeSend %d went out by fork, so the pipe path was not exercised", i)
		}
	}

	sessions := clientSessions(t, socket)
	if len(sessions) == 0 {
		t.Fatal("no client at all, so the pipe path never opened one and this proves nothing")
	}
	for _, session := range sessions {
		if session != anchorSession {
			t.Fatalf("the manager holds a client of session %q; only %q is its own",
				session, anchorSession)
		}
	}
	if got := controlClientsOn(t, socket, "user"); got != 0 {
		t.Fatalf("%d control clients on the adopted pane's own session", got)
	}
	// Exactly one, not merely under the cap. The cap is a backstop against a
	// bug; the design says there is a single pooled client per server, and a
	// second one appearing is the shape of the outage returning even while
	// the count stays legal.
	if got := driver.ControlClientsOn(socket); got != 1 {
		t.Fatalf("the manager holds %d control clients on one server, want exactly 1 pooled one", got)
	}
}
