// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package tmux

import (
	"errors"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer feeds scripted control-mode output to a Control and records
// what the client writes, standing in for a tmux server.
type fakeServer struct {
	control *Control
	writes  *syncBuf
	feed    io.WriteCloser
}

func newFakeServer() *fakeServer {
	stdoutRead, stdoutWrite := io.Pipe()
	writes := &syncBuf{}
	return &fakeServer{
		control: newControl(writes, stdoutRead),
		writes:  writes,
		feed:    stdoutWrite,
	}
}

// syncBuf records the client's writes; Command writes from test goroutines
// while assertions read, so access is locked.
type syncBuf struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *syncBuf) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(data)
}

func (s *syncBuf) Close() error { return nil }

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (f *fakeServer) send(lines ...string) {
	io.WriteString(f.feed, strings.Join(lines, "\n")+"\n")
}

// waitWritten blocks until the client's stdin contains want, so a scripted
// reply cannot outrun the waiter it is meant to resolve.
func waitWritten(t *testing.T, server *fakeServer, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !strings.Contains(server.writes.String(), want) {
		select {
		case <-deadline:
			t.Fatalf("command %q never written to stdin", want)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestControlCommandRoundTrip(t *testing.T) {
	server := newFakeServer()
	// tmux greets every control client with an unsolicited empty block.
	server.send("%begin 1 0 0", "%end 1 0 0")

	type result struct {
		text string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		text, err := server.control.Command("capture-pane -p")
		got <- result{text, err}
	}()

	waitWritten(t, server, "capture-pane -p\n")
	if want := "capture-pane -p\n"; server.writes.String() != want {
		t.Fatalf("stdin = %q, want %q", server.writes.String(), want)
	}

	server.send("%begin 2 1 0", "line one", "line two", "%end 2 1 0")
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("Command: %v", r.err)
		}
		if r.text != "line one\nline two" {
			t.Fatalf("reply = %q", r.text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reply never resolved")
	}
}

// Pane text is echoed raw inside reply blocks, so a pane that happens to
// display the control protocol (a diff of this file, tmux docs) must not
// terminate the block early: only the %end carrying the block's own tag
// does. A mismatched %end taken as a terminator shifts every later reply
// onto the wrong caller for the life of the client.
func TestControlBlockSurvivesProtocolLookalikes(t *testing.T) {
	server := newFakeServer()
	server.send("%begin 1 0 0", "%end 1 0 0")

	type result struct {
		text string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		text, err := server.control.Command("capture-pane -p")
		got <- result{text, err}
	}()
	waitWritten(t, server, "capture-pane -p\n")
	server.send(
		"%begin 2 1 0",
		"%end 123 456 0",
		"%error 123 456 0",
		"%begin 999 999 0",
		"%output fake",
		"after",
		"%end 2 1 0",
	)
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("Command: %v", r.err)
		}
		want := "%end 123 456 0\n%error 123 456 0\n%begin 999 999 0\n%output fake\nafter"
		if r.text != want {
			t.Fatalf("reply = %q, want %q", r.text, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reply never resolved")
	}
	// The queue stayed aligned: a second command gets the second reply.
	got2 := make(chan result, 1)
	go func() {
		text, err := server.control.Command("display-message -p x")
		got2 <- result{text, err}
	}()
	waitWritten(t, server, "display-message -p x\n")
	server.send("%begin 3 2 0", "second", "%end 3 2 0")
	select {
	case r := <-got2:
		if r.err != nil || r.text != "second" {
			t.Fatalf("second reply = %q, %v", r.text, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second reply never resolved")
	}
}

// A server that stops answering must cost the caller a bounded wait, not
// hang it: Command runs on the UI loop.
//
// The budget is overridden rather than waited out: this pins that the
// deadline fires and that Command spends it first. The production length is
// pinned by TestControlTimeoutDefaultsToTheProductionBudget below.
func TestControlCommandTimesOut(t *testing.T) {
	budget := 60 * time.Millisecond
	server := newFakeServer()
	server.control.timeoutOverride = budget
	server.send("%begin 1 0 0", "%end 1 0 0")
	start := time.Now()
	_, err := server.control.Command("capture-pane -p")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	// The lower bound is the half of this that a returned error alone does
	// not prove: a Command that gave up immediately would report the same
	// timeout without ever having waited for the reply it was sent for.
	waited := time.Since(start)
	if waited < budget {
		t.Fatalf("Command gave up after %v, want it to spend its whole %v budget", waited, budget)
	}
	if waited > budget+time.Second {
		t.Fatalf("timeout took %v", waited)
	}
}

// The override above only means anything while an un-overridden Control
// still waits the production budget, so that default is pinned here. The
// length is pinned too: overriding it everywhere that used to wait it out
// means a bad edit to the constant would otherwise slow the UI down with
// nothing left in the suite to notice.
func TestControlTimeoutDefaultsToTheProductionBudget(t *testing.T) {
	var control Control
	if got := control.replyTimeout(); got != commandTimeout {
		t.Fatalf("a Control with no override waits %v, want the production %v", got, commandTimeout)
	}
	if commandTimeout != 2*time.Second {
		t.Fatalf("commandTimeout = %v: the UI loop blocks on this, so changing it is a deliberate act", commandTimeout)
	}
}

func TestControlErrorBlock(t *testing.T) {
	server := newFakeServer()
	server.send("%begin 1 0 0", "%end 1 0 0")

	got := make(chan error, 1)
	go func() {
		_, err := server.control.Command("bogus-command")
		got <- err
	}()
	waitWritten(t, server, "bogus-command\n")
	server.send("%begin 2 1 0", "unknown command: bogus-command", "%error 2 1 0")
	select {
	case err := <-got:
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("err = %v, want unknown command", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("error reply never resolved")
	}
}

// TestControlOutputEvent and TestControlEventsCoalesce lived here until
// 2026-08-27. They pinned the %output notification channel, which existed
// only to tell the per-session mirror that a pane had painted. The mirror
// was what killed the operator's tmux server, so it was deleted outright and
// the one client left attaches with no-output; there is no event to coalesce
// and nothing left to push one. The parser still has to survive pane text
// that looks like a notification, and TestControlBlockSurvivesProtocolLookalikes
// above is what pins that.

func TestControlServerExitWakesWaiters(t *testing.T) {
	server := newFakeServer()
	got := make(chan error, 1)
	go func() {
		_, err := server.control.Command("capture-pane -p")
		got <- err
	}()
	time.Sleep(10 * time.Millisecond)
	server.feed.Close()
	select {
	case err := <-got:
		if err == nil {
			t.Fatal("waiter resolved without error after exit")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter hung after server exit")
	}
	select {
	case <-server.control.Done():
	default:
		t.Fatal("Done not closed after exit")
	}
	if _, err := server.control.Command("anything"); err == nil {
		t.Fatal("Command after exit must fail")
	}
}

// Integration: a real control client against the isolated test server.
// Proves the fork-free capture path -- a command issued over the pooled poll
// client, which is attached to the manager's anchor and not to the session
// being read, still reads that session's pane.
func TestPollControlCapturesAManagedPaneOverThePipe(t *testing.T) {
	driver := requireTmux(t)
	id := uniqueID("ctl")
	if err := driver.Create(id, "/tmp", "", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	control, err := driver.OpenPollControl(testSocket)
	if err != nil {
		t.Fatalf("OpenPollControl: %v", err)
	}
	t.Cleanup(func() { control.Close(); driver.closeAnchors() })

	if _, err := control.Command("capture-pane -p -e -t " + sessionName(id)); err != nil {
		t.Fatalf("capture over control pipe: %v", err)
	}

	// What the deleted event channel used to wait for, asked for instead:
	// the pane paints and the next capture over the same pipe sees it. The
	// poll is what the program does now, and it costs no process per read.
	if err := driver.SendText(id, "echo control-mode-ping"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		text, err := control.Command("capture-pane -p -t " + sessionName(id))
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if strings.Contains(text, "control-mode-ping") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture never showed pane text: %q", text)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Benchmarks the decision that motivates control mode: capture over the
// persistent pipe versus one exec fork per capture.
func BenchmarkCaptureControlPipe(b *testing.B) {
	driver, control, id := benchControl(b)
	defer control.Close()
	defer driver.Kill(id)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := control.Command("capture-pane -p -e -t gi_" + id); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCaptureExecFork(b *testing.B) {
	driver, control, id := benchControl(b)
	control.Close()
	defer driver.Kill(id)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// runAt, not CapturePane: CapturePane now prefers the pooled pipe
		// and would measure the other arm of this comparison.
		if _, err := driver.runAt(id, "capture-pane", "-p", "-e", "-t", driver.TargetName(id)); err != nil {
			b.Fatal(err)
		}
	}
}

func benchControl(b *testing.B) (*Driver, *Control, string) {
	b.Helper()
	driver, err := NewWithSocket(testSocket)
	if err != nil {
		b.Fatal(err)
	}
	id := "bench" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	if err := driver.Create(id, "/tmp", "", nil, 200, 50); err != nil {
		b.Fatal(err)
	}
	control, err := driver.OpenPollControl(testSocket)
	if err != nil {
		b.Fatal(err)
	}
	return driver, control, id
}

// operatorServer stands in for the tmux server the operator works on: their
// own session, two windows, and a real client attached to it and looking at
// the first window.
//
// The client matters. Everything this file guards is about a session with
// somebody in it: clients of one session all show that session's current
// window, so a second client is what turns a moved window into a moved view.
// A person at a keyboard is a client with a terminal, and a pane on a second
// tmux server is the only terminal a test has to hand one.
func operatorServer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := tmuxtest.NewSocket("operator")
	tmuxOn(socket, "kill-server").Run()
	t.Cleanup(func() { tmuxOn(socket, "kill-server").Run() })
	run := func(args ...string) {
		t.Helper()
		if out, err := tmuxOn(socket, args...).CombinedOutput(); err != nil {
			t.Fatalf("operator tmux %v: %v: %s", args, err, out)
		}
	}
	run("new-session", "-d", "-s", operatorSession, "-x", "80", "-y", "24", "cat")
	run("new-window", "-t", operatorSession, "cat")
	run("select-window", "-t", operatorSession+":0")

	terminal := tmuxtest.NewSocket("terminal")
	tmuxOn(terminal, "kill-server").Run()
	t.Cleanup(func() { tmuxOn(terminal, "kill-server").Run() })
	if out, err := tmuxOn(terminal, "new-session", "-d", "-s", "terminal", "-x", "80", "-y", "24",
		"tmux -L "+socket+" attach-session -t "+operatorSession).CombinedOutput(); err != nil {
		t.Fatalf("operator terminal: %v: %s", err, out)
	}
	waitForClients(t, socket, 1)
	return socket
}

// operatorSession is named the way the operator's own session actually is on
// the machine this regression was found on.
const operatorSession = "main"

// sessionClients is every client of the operator's session, one line of
// "<flags> control=<0|1>" each.
func sessionClients(t *testing.T, socket string) []string {
	t.Helper()
	out, err := tmuxOn(socket, "list-clients", "-t", operatorSession,
		"-F", "#{client_flags} control=#{client_control_mode}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-clients: %v: %s", err, out)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func waitForClients(t *testing.T, socket string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := sessionClients(t, socket); len(got) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("clients of %s never settled at %d: %q", operatorSession, want, sessionClients(t, socket))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// currentWindow is the window every client of the operator's session is
// looking at.
func currentWindow(t *testing.T, socket string) string {
	t.Helper()
	out, err := tmuxOn(socket, "display-message", "-p", "-t", operatorSession, "#{window_index}").CombinedOutput()
	if err != nil {
		t.Fatalf("display-message: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// The regression this file exists for. The poll client attaches to a whole
// server, and an attach with no -t lands on whichever session the server used
// last -- which on the operator's own server is the session they are working
// in. It became a client of their session, and a client of a session follows
// that session's current window, so the manager could walk their terminal to
// another window with nobody touching a key.
func TestPollControlNeverAttachesToTheOperatorsSession(t *testing.T) {
	driver := requireTmux(t)
	socket := operatorServer(t)
	// The operator's session is the one tmux used last, which is exactly the
	// session an attach with no -t would take.
	if got := sessionClients(t, socket); len(got) != 1 {
		t.Fatalf("fixture should start with the operator's client alone: %q", got)
	}

	control, err := driver.OpenPollControl(socket)
	if err != nil {
		t.Fatalf("OpenPollControl: %v", err)
	}
	t.Cleanup(func() { control.Close(); driver.closeAnchors() })
	// The client is attached by the time it answers a command.
	if _, err := control.Command("list-sessions -F '#{session_name}'"); err != nil {
		t.Fatalf("poll command: %v", err)
	}

	for _, client := range sessionClients(t, socket) {
		if strings.Contains(client, "control=1") {
			t.Errorf("the manager attached a control client to the operator's session: %q", client)
		}
	}
	if got := sessionClients(t, socket); len(got) != 1 {
		t.Errorf("clients of the operator's session = %q, want the operator's own alone", got)
	}
	if got := currentWindow(t, socket); got != "0" {
		t.Errorf("the operator's view moved to window %s", got)
	}

	// It went somewhere, and somewhere is the manager's own anchor.
	anchored, err := tmuxOn(socket, "list-clients", "-t", anchorSession,
		"-F", "#{client_control_mode}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-clients on the anchor: %v: %s", err, anchored)
	}
	if got := strings.TrimSpace(string(anchored)); got != "1" {
		t.Errorf("control clients on the anchor = %q, want the poll client", got)
	}

	// And it still reads panes in the session it is not attached to, which
	// is the only reason it is on this server at all.
	pane, err := control.Command("capture-pane -p -t " + operatorSession)
	if err != nil {
		t.Fatalf("capture across sessions: %v", err)
	}
	_ = pane
}

// The regression the 2026-08-27 outage left behind, and the invariant that
// would have prevented it: the manager must never become a client of a
// session it did not create.
//
// It used to mirror an adopted pane by attaching to whatever session held it,
// which on the operator's own server is the session they are working in. One
// control client per focus change went into their tmux server; the log shows
// 584 opens against "main" and "claude" and no closes, and the server died
// with every live session on it.
//
// So the assertion is not about flags or window sizes any more. It is a
// count: after asking to attach against a pane in the operator's session,
// their session must have exactly the clients it started with, and none of
// them in control mode.
//
// The mirror that made the ask is gone, so the ask is made where the refusal
// now lives -- startControl, the one place a control client is built. Going
// through it directly is deliberate: it is the last line of defence, and a
// test that only exercised the callers above it would go green the day
// somebody adds a fifth one.
func TestControlRefusesToBecomeAClientOfTheOperatorsSession(t *testing.T) {
	driver := requireTmux(t)
	socket := operatorServer(t)
	out, err := tmuxOn(socket, "list-panes", "-t", operatorSession+":1", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes: %v: %s", err, out)
	}
	pane := strings.TrimSpace(string(out))
	before := sessionClients(t, socket)

	// Adopting the pane is what used to hand the mirror the operator's
	// session name, and it is still how the manager comes to hold a pane it
	// did not create, so the fixture stays honest about where the name the
	// refusal has to reject comes from.
	id := uniqueID("adopted")
	if err := driver.Adopt(id, Target{Socket: socket, Name: pane}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	control, err := driver.startControl(socket, operatorSession, pollFlags)
	if err == nil {
		control.Close()
		t.Fatal("startControl joined a session the manager does not own")
	}
	if !errors.Is(err, ErrAdopted) {
		t.Fatalf("attach to the operator's session: want ErrAdopted, got %v", err)
	}
	if got := driver.ControlClientsOn(socket); got != 0 {
		t.Fatalf("the manager holds %d control clients on the operator's server", got)
	}
	after := sessionClients(t, socket)
	if len(after) != len(before) {
		t.Fatalf("the operator's session gained a client: %q -> %q", before, after)
	}
	for _, line := range after {
		if strings.Contains(line, "control=1") {
			t.Fatalf("a control client is attached to the operator's own session: %q", after)
		}
	}
	if got := currentWindow(t, socket); got != "0" {
		t.Fatalf("the operator's view moved to window %s", got)
	}
}

// The refusal has to hold at the one place every control client is built,
// so a future caller that resolves a session name some other way cannot walk
// around it. The table is the operator's real session names plus the two
// shapes a bug produces: no name at all, and a name that merely contains the
// manager's prefix without starting with it.
func TestStartControlRefusesAnUnownedSession(t *testing.T) {
	driver, _ := stubTmux(t)
	for _, session := range []string{"main", "claude", "", "notgi_"} {
		if _, err := driver.startControl("amsomesocket", session, pollFlags); err == nil {
			t.Fatalf("startControl attached to session %q, which the manager did not create", session)
		}
	}
	if _, err := driver.startControl("amsomesocket", anchorSession, pollFlags); err != nil {
		t.Fatalf("startControl refused its own anchor session: %v", err)
	}
}

func TestAnchorSessionIsReusedAndCleanedUp(t *testing.T) {
	driver := requireTmux(t)
	socket := operatorServer(t)
	anchors := func() []string {
		t.Helper()
		out, err := tmuxOn(socket, "list-sessions", "-F", "#{session_name}").CombinedOutput()
		if err != nil {
			t.Fatalf("list-sessions: %v: %s", err, out)
		}
		var found []string
		for _, name := range strings.Fields(string(out)) {
			if name == anchorSession {
				found = append(found, name)
			}
		}
		return found
	}

	for pass := 0; pass < 3; pass++ {
		control, err := driver.OpenPollControl(socket)
		if err != nil {
			t.Fatalf("pass %d: OpenPollControl: %v", pass, err)
		}
		if _, err := control.Command("list-sessions -F '#{session_name}'"); err != nil {
			t.Fatalf("pass %d: poll command: %v", pass, err)
		}
		if got := anchors(); len(got) != 1 {
			t.Fatalf("pass %d: anchor sessions = %q, want exactly one", pass, got)
		}
		control.Close()
	}
	driver.closeAnchors()
	if got := anchors(); len(got) != 0 {
		t.Fatalf("the anchor outlived the manager: %q", got)
	}
	// Killing the anchor is the manager's own housekeeping and nothing else's.
	if got := sessionClients(t, socket); len(got) != 1 {
		t.Fatalf("cleanup disturbed the operator's session: %q", got)
	}
}

// A control client with no -t is the bug, so no path may build one. The
// assertion is on the command line rather than on a caller, because the
// argument is the thing that was missing.
func TestEveryControlAttachNamesASession(t *testing.T) {
	driver := requireTmux(t)
	socket := operatorServer(t)
	calls := countingTmux(t, driver)

	control, err := driver.OpenPollControl(socket)
	if err != nil {
		t.Fatalf("OpenPollControl: %v", err)
	}
	t.Cleanup(func() { control.Close(); driver.closeAnchors() })
	if _, err := control.Command("list-sessions -F '#{session_name}'"); err != nil {
		t.Fatalf("poll command: %v", err)
	}

	var attaches int
	for _, call := range calls() {
		if !strings.Contains(call, "attach-session") {
			continue
		}
		attaches++
		if !strings.Contains(call, " -t ") {
			t.Errorf("attach with no target: %q", call)
		}
		if strings.Contains(call, "-t "+operatorSession) {
			t.Errorf("attach aimed at the operator's session: %q", call)
		}
	}
	if attaches == 0 {
		t.Fatal("no attach was recorded, so the assertion proved nothing")
	}
	if _, err := startControlOnEmptyTarget(driver, socket); err == nil {
		t.Error("startControl accepted an empty session and attached without a target")
	}
}

func startControlOnEmptyTarget(driver *Driver, socket string) (*Control, error) {
	return driver.startControl(socket, "  ", pollFlags)
}

// Putting up the anchor must not put up a tmux server. new-session starts one
// where none is running, so an unconditional create would resurrect every
// server a dead socket names -- and a pane read there would then answer with
// the anchor's own pane rather than failing.
func TestPollControlDoesNotStartADeadServer(t *testing.T) {
	driver := requireTmux(t)
	socket := tmuxtest.NewSocket("gone")
	if _, err := driver.OpenPollControl(socket); err == nil {
		t.Fatal("opened a poll client on a server that is not running")
	}
	out, err := tmuxOn(socket, "list-sessions").CombinedOutput()
	if err == nil {
		t.Fatalf("the manager started a tmux server that was not running: %s", out)
	}
	if !noServer(string(out)) {
		t.Fatalf("list-sessions on the dead socket said %q", strings.TrimSpace(string(out)))
	}
}

// The anchor is a session on a server the operator can do as they like with,
// so it will be killed sometimes. tmux ends the client when that happens,
// which the pool already notices -- and the reopen has to put the anchor back
// rather than attach to whatever session is nearest.
func TestAKilledAnchorIsRebuiltNotAbandoned(t *testing.T) {
	driver := requireTmux(t)
	socket := operatorServer(t)
	t.Cleanup(driver.CloseCaptureClients)

	control, err := driver.captures.client(driver, socket)
	if err != nil {
		t.Fatalf("first client: %v", err)
	}
	if _, err := control.Command("list-sessions -F '#{session_name}'"); err != nil {
		t.Fatalf("poll command: %v", err)
	}

	if out, err := tmuxOn(socket, "kill-session", "-t", anchorSession).CombinedOutput(); err != nil {
		t.Fatalf("kill the anchor: %v: %s", err, out)
	}
	select {
	case <-control.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the client outlived the anchor session it was attached to")
	}

	reopened, err := driver.captures.client(driver, socket)
	if err != nil {
		t.Fatalf("reopen after the anchor died: %v", err)
	}
	if reopened == control {
		t.Fatal("the pool handed back the dead client")
	}
	if _, err := reopened.Command("list-sessions -F '#{session_name}'"); err != nil {
		t.Fatalf("reopened poll command: %v", err)
	}
	// Rebuilt, and rebuilt as an anchor: the operator's session is still
	// theirs alone.
	for _, client := range sessionClients(t, socket) {
		if strings.Contains(client, "control=1") {
			t.Errorf("the reopen landed on the operator's session: %q", client)
		}
	}
	anchored, err := tmuxOn(socket, "list-clients", "-t", anchorSession, "-F", "#{client_control_mode}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-clients on the rebuilt anchor: %v: %s", err, anchored)
	}
	if got := strings.TrimSpace(string(anchored)); got != "1" {
		t.Errorf("control clients on the rebuilt anchor = %q, want the poll client", got)
	}
}
