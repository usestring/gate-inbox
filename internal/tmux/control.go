// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package tmux

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/tmuxguard"
)

// Control is a persistent control-mode client attached to one session.
// Commands ride the pipe and their replies come back on it, so reading a
// pane costs a round trip rather than a forked tmux process. It reads no
// pushed output: the one client left attaches with no-output -- see
// pollFlags, and "The mirror is gone" for why nothing pushes any more.
type Control struct {
	cmd *exec.Cmd

	// socket and session name what this client attached to, for the close
	// log. An open line that cannot be paired with a close line is how the
	// leak described under "The mirror is gone" stayed invisible in a day
	// of logs.
	socket  string
	session string

	// closeOnce guards the hook below, so the socket's client slot is given
	// back once however many times Close is called and whoever calls it.
	closeOnce sync.Once
	onClose   func()

	// writeMu serializes stdin writes and, held across the queue append,
	// keeps write order identical to waiter order. Writes never run under
	// mu: the read loop needs mu to resolve replies, and a write blocked
	// on a full pipe while holding it would stop stdout draining - a
	// cycle no timeout breaks.
	writeMu sync.Mutex
	stdin   io.WriteCloser

	// timeoutOverride replaces commandTimeout when non-zero. It exists for
	// the tests that assert the deadline fires: read it through
	// replyTimeout, never directly, so zero keeps production behaviour.
	timeoutOverride time.Duration

	mu      sync.Mutex
	pending []chan reply
	closed  bool
	// greeted flips once the connect-time block has been consumed; until
	// then no reply block may resolve a command waiter.
	greeted bool

	// done closes when the control client exits (detach, kill, or error).
	done    chan struct{}
	exitErr error
}

type reply struct {
	text string
	err  error
}

// pollFlags are the client flags the poll client attaches with.
//
// ignore-size is what makes a control client safe on a server somebody is
// working in: without it a window whose window-size is "latest" would follow
// whichever client attached last, and this one carries no terminal to size
// it by. no-output drops %output entirely -- this client issues captures and
// never reads pushed bytes, and a board of busy agents would otherwise
// stream every character they paint down the pipe.
const pollFlags = "ignore-size,no-output"

// anchorSession is the session the poll client attaches to, one per tmux
// server. It exists for no other reason: a control client has to attach
// somewhere, and every other session on the operator's default server is
// either an agent or their own work.
//
// The name carries the manager's prefix so it reads as ours in list-sessions,
// and a suffix no session id can produce - ids are eight hex characters - so
// it can be excluded from the managed sessions by name without ever hiding a
// real one.
const anchorSession = prefix + "poll-anchor"

// anchorCommand is what runs in the anchor's single pane.
//
// cat blocks forever on a pty nothing writes to, which is the cheapest way to
// hold a session open: no shell, no rc files, no process tree, and nothing
// the adoption scan could mistake for an agent.
//
// The two printed lines are for the operator who runs list-sessions, finds an
// gi_ session they did not start, and deserves to learn in one line what it is
// and that killing it costs nothing.
const anchorCommand = `printf '%s\n' ` +
	`'Gate Inbox: idle anchor for its tmux control client.' ` +
	`'Nothing runs here. Safe to kill; the manager makes another.'; exec cat`

// maxControlClientsPerSocket is the hard ceiling on control-mode clients
// this manager may hold on any one tmux server.
//
// It is a backstop, not a budget. There is one kind of client left -- the
// poll client, one per server, attached to the manager's own anchor -- so
// the real number is one, and this is what makes a bug that opens them in a
// loop cost a degraded preview instead of the tmux server an unbounded
// version of this code already destroyed once. See "The mirror is gone".
//
// Over the cap the caller is refused and forks instead, which reads the same
// bytes and needs no client at all.
const maxControlClientsPerSocket = 4

// MaxControlClientsPerSocket exports the ceiling so a test can assert against
// the number the code actually enforces rather than a copy of it.
const MaxControlClientsPerSocket = maxControlClientsPerSocket

// ErrControlAtCap is the refusal that keeps the ceiling structural. A caller
// that sees it has a working alternative -- the capture path -- and must take
// it rather than retrying.
var ErrControlAtCap = errors.New("control clients at the per-socket cap")

// controlPool counts the control clients the driver has open per server, so
// maxControlClientsPerSocket is enforced in one place rather than in each
// caller's good intentions.
type controlPool struct {
	mu     sync.Mutex
	counts map[string]int
}

// reserve takes one slot in a socket's client budget, or refuses.
func (p *controlPool) reserve(socket string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reserveLocked(socket)
}

func (p *controlPool) reserveLocked(socket string) bool {
	if p.counts == nil {
		p.counts = map[string]int{}
	}
	if p.counts[socket] >= maxControlClientsPerSocket {
		return false
	}
	p.counts[socket]++
	return true
}

// release gives a slot back. Called from Control.Close, once per client.
func (p *controlPool) release(socket string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.counts[socket] > 0 {
		p.counts[socket]--
	}
}

// live reports how many control clients this manager holds on a socket.
// Exported through Driver for the tests that pin the ceiling.
func (p *controlPool) live(socket string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[socket]
}

// ControlClientsOn reports how many control-mode clients this manager holds
// on one tmux server. It exists so the cap can be asserted rather than
// believed.
func (d *Driver) ControlClientsOn(socket string) int { return d.controls.live(socket) }

// ensureAnchor makes sure the anchor session exists on a server, creating it
// if it does not. A stale anchor -- from an earlier pass, or from a run that
// died before it could clean up -- is reused, which is what keeps the manager
// from accumulating one anchor per launch.
//
// The listing first is not a nicety. new-session starts a tmux server when
// none is running, so creating the anchor unconditionally would resurrect the
// server every dead socket names, and a pane read on it would then answer
// with the anchor's own pane instead of failing. A server that is not running
// is a refusal here, which the caller reads as "no client for this one".
//
// Reuse is the refusal to create a second anchor, never an attach:
// new-session -A would attach to the session it found, and the flag that
// stops it doing so (-D) detaches whatever is already attached, which on
// reopen is a client of ours.
func (d *Driver) ensureAnchor(socket string) error {
	tmuxguard.Enforce([]string{"-L", socket})
	out, err := exec.Command(d.bin, "-L", socket, "list-sessions", "-F", "#{session_name}").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux anchor session on %s: %w: %s", socket, err, strings.TrimSpace(string(out)))
	}
	present := false
	// By line, not by field: a tmux session name may contain spaces, and a
	// session called "notes gi_poll-anchor" would otherwise read as ours and
	// leave the poll client with nothing to attach to.
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(name) == anchorSession {
			present = true
			break
		}
	}
	if !present {
		made, err := exec.Command(d.bin, "-L", socket, "new-session", "-d",
			"-s", anchorSession, "sh -c "+ShellQuote(anchorCommand)).CombinedOutput()
		// A second manager racing this one wins the name, which is the
		// answer either of them wanted.
		if err != nil && !strings.Contains(string(made), "duplicate session") {
			return fmt.Errorf("tmux anchor session on %s: %w: %s", socket, err, strings.TrimSpace(string(made)))
		}
	}
	d.anchorsMu.Lock()
	if d.anchors == nil {
		d.anchors = map[string]bool{}
	}
	d.anchors[socket] = true
	d.anchorsMu.Unlock()
	return nil
}

// closeAnchors kills every anchor this manager put up. Run after the clients
// attached to them are gone, so tmux is not racing a detach against a
// kill-session. A server that has already exited is not an error: the anchor
// went with it, and neither is a second manager still using the name -- its
// client sees the session go and opens a new one, anchor and all.
func (d *Driver) closeAnchors() {
	d.anchorsMu.Lock()
	sockets := d.anchors
	d.anchors = nil
	d.anchorsMu.Unlock()
	for socket := range sockets {
		tmuxguard.Enforce([]string{"-L", socket})
		exec.Command(d.bin, "-L", socket, "kill-session", "-t", anchorSession).Run()
	}
}

// The mirror is gone.
//
// Until 2026-08-27 the focused pane was watched by a control-mode client
// attached to the session that held it, so tmux would push an %output event
// on every paint. tmux pushes %output only for panes in the client's own
// session -- measured, not assumed -- so watching a pane the operator owned
// meant becoming a client of the operator's own session. One per focus
// change reached 584 clients and killed their server, taking every live
// agent with it.
//
// The first fix refused adopted panes, which left 23 of the operator's 25
// panes with no mirror and a 300ms timer as their only source of frames. The
// second fix, this one, deleted the mirror instead: measured on a real pane,
// a mirror push echoed a keystroke in 27ms and simply asking over the pooled
// per-server pipe echoed it in 0.5ms. The mirror was never the fast path --
// it debounced paints for 25ms before capturing anyway -- it was only the
// path that avoided a fork, and the pooled client avoids the fork without
// attaching to anything the manager did not create.
//
// What remains is the poll client below: one per tmux server, attached to
// the manager's own anchor session, carrying commands for every pane on that
// server. See pipe.go for how the focused reads use it.

// OpenPollControl attaches one client to a whole tmux server. Commands ride
// the pipe for every pane on the server regardless of which session the
// client sits in, so the client needs a session only to have somewhere to
// attach - and it attaches to one the manager makes for the purpose.
//
// It must never be the session tmux would have picked. An attach with no -t
// lands on the server's most recently used session, which on the operator's
// own default server is the session they are working in; sharing a session
// means sharing its current window, so that client could drag their view to
// another window under them. The anchor is a session nobody is looking at.
//
// A killed anchor ends the client, and the next pass opens a new one - see
// ensureAnchor, which the reopen runs through as well, so the recovery
// recreates the anchor rather than going without a target.
func (d *Driver) OpenPollControl(socket string) (*Control, error) {
	if err := d.ensureAnchor(socket); err != nil {
		return nil, err
	}
	// The poll client counts against the same ceiling as everything else.
	// It is one per server and the pool holds it, so it only ever refuses
	// when something upstream has already gone wrong -- which is exactly when
	// the ceiling has to hold.
	if !d.controls.reserve(socket) {
		logging.Warn("tmux control clients at cap",
			"socket", socket, "session", anchorSession,
			"live", d.controls.live(socket), "cap", maxControlClientsPerSocket)
		return nil, ErrControlAtCap
	}
	control, err := d.startControl(socket, anchorSession, pollFlags)
	if err != nil {
		d.controls.release(socket)
		return nil, err
	}
	return control, nil
}

// startControl attaches one control-mode client to one session. Every
// control client in the program is built here and the -t is a parameter
// rather than part of a caller's argument list, which is what keeps "never
// attach without a target" a property of the code instead of a habit.
func (d *Driver) startControl(socket, session, flags string) (*Control, error) {
	if strings.TrimSpace(session) == "" {
		return nil, fmt.Errorf("control attach on %q: no session to attach to", socket)
	}
	// The invariant the outage cost: this manager attaches only to sessions
	// it named itself. Managed sessions are "gi_<id>" and the poll anchor is
	// "gi_poll-anchor"; the operator's own "main" and "claude" are not, and
	// neither is any pane adopted out of them. It lives here because every
	// control client in the program is built here, so this cannot be a rule
	// somebody remembers to follow at four call sites.
	if !strings.HasPrefix(session, prefix) {
		return nil, fmt.Errorf("control attach on %q: refusing to become a client of session %q, which this manager did not create: %w",
			socket, session, ErrAdopted)
	}
	args := []string{"attach-session", "-t", session, "-f", flags}
	logging.Info("tmux control open", "socket", socket, "cmd", strings.Join(args, " "))
	tmuxguard.Enforce([]string{"-L", socket})
	cmd := exec.Command(d.bin, append([]string{"-L", socket, "-C"}, args...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("control stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("control stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		logging.Warn("tmux control open failed", "socket", socket, "err", err.Error())
		return nil, fmt.Errorf("control attach: %w", err)
	}
	control := newControl(stdin, stdout)
	control.cmd = cmd
	control.socket, control.session = socket, session
	control.onClose = func() { d.controls.release(socket) }
	// A client can also end without anybody closing it: the anchor is killed,
	// the server exits, the pane goes. That is still a close as far as the
	// budget and the log are concerned, so it reaps itself.
	go func() {
		<-control.done
		control.reap("exited")
	}()
	return control, nil
}

// newControl wires the protocol loop over raw pipes. Split from the attach
// so tests can drive the parser without a tmux server.
func newControl(stdin io.WriteCloser, stdout io.Reader) *Control {
	control := &Control{
		stdin: stdin,
		done:  make(chan struct{}),
	}
	go control.readLoop(stdout)
	return control
}

// Done closes when the client exits; Err then reports why.
func (c *Control) Done() <-chan struct{} { return c.done }

func (c *Control) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitErr
}

// commandTimeout bounds how long a Command waits for its reply. Commands
// run synchronously on the UI loop, so a tmux server that stops answering
// must cost a beat, never a frozen interface.
const commandTimeout = 2 * time.Second

// replyTimeout is the budget every wait on the server actually spends.
// Production leaves timeoutOverride at zero and so gets commandTimeout; the
// override exists so a test can watch the deadline fire without waiting it.
func (c *Control) replyTimeout() time.Duration {
	if c.timeoutOverride > 0 {
		return c.timeoutOverride
	}
	return commandTimeout
}

// Command runs one tmux command over the control pipe and returns its
// output. Replies arrive strictly in command order, so each call enqueues
// a waiter that the read loop resolves from the front of the queue.
func (c *Control) Command(command string) (string, error) {
	waiter, err := c.submit(command)
	if err != nil {
		return "", err
	}
	timeout := time.NewTimer(c.replyTimeout())
	defer timeout.Stop()
	select {
	case result := <-waiter:
		return result.text, result.err
	case <-c.done:
		return "", ErrControlExited
	case <-timeout.C:
		// The waiter stays queued: if the reply does arrive later it pops
		// this abandoned (buffered) channel and the queue stays aligned.
		return "", ErrControlTimedOut
	}
}

// Reply is one command's answer inside a Batch. Err is the command's own
// failure - a pane that has closed, a bad target - and says nothing about
// the pipe, which Batch reports separately.
type Reply struct {
	Text string
	Err  error
}

// ErrControlExited and ErrControlTimedOut are the two ways the pipe itself
// fails a batch, as opposed to a single command failing on the server.
var (
	ErrControlExited   = fmt.Errorf("control client exited")
	ErrControlTimedOut = fmt.Errorf("control reply timed out")
)

// Batch runs a list of commands over one pipe, writing every one before it
// reads any reply, so a whole board's captures cost one round trip instead
// of one each. Writing first is safe because a capture command is ~45 bytes
// and a pipe buffer is 64KB: a board would have to reach four figures
// before the unread writes could block.
//
// deadline covers the entire list: a server that stops answering costs one
// wait, not one per command. The second return is non-nil only when the
// pipe failed, which is the only reason to throw the client away - a
// command that failed on its own leaves the client good.
func (c *Control) Batch(commands []string, deadline time.Duration) ([]Reply, error) {
	replies := make([]Reply, len(commands))
	waiters := make([]chan reply, len(commands))
	var fatal error
	for i, command := range commands {
		waiter, err := c.submit(command)
		if err != nil {
			fatal = err
			break
		}
		waiters[i] = waiter
	}
	timeout := time.NewTimer(deadline)
	defer timeout.Stop()
	for i, waiter := range waiters {
		if fatal != nil || waiter == nil {
			replies[i].Err = fatal
			continue
		}
		select {
		case result := <-waiter:
			replies[i] = Reply{Text: result.text, Err: result.err}
		case <-c.done:
			fatal = ErrControlExited
			replies[i].Err = fatal
		case <-timeout.C:
			// Nothing later in the list can beat a deadline that has
			// already passed, so the rest of the batch fails with it
			// rather than waiting again on a spent timer.
			fatal = ErrControlTimedOut
			replies[i].Err = fatal
		}
	}
	return replies, fatal
}

// Send writes one tmux command and returns without waiting for its reply.
// For keystroke forwarding: once the write lands the pane owns the key,
// and waiting for the acknowledgement would only stall the caller. The
// discarded reply still pops this command's own waiter, so the queue
// stays aligned.
func (c *Control) Send(command string) error {
	_, err := c.submit(command)
	return err
}

// submit enqueues a reply waiter and writes the command line. The queue
// append rides inside the write lock so waiter order always matches write
// order, while mu itself is never held across the pipe write.
func (c *Control) submit(command string) (chan reply, error) {
	waiter := make(chan reply, 1)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("control client closed")
	}
	c.pending = append(c.pending, waiter)
	c.mu.Unlock()
	if _, err := io.WriteString(c.stdin, command+"\n"); err != nil {
		// Nothing went out, so no reply will come: leaving the waiter
		// queued would shift every later reply onto the wrong caller.
		c.mu.Lock()
		for i := len(c.pending) - 1; i >= 0; i-- {
			if c.pending[i] == waiter {
				c.pending = append(c.pending[:i], c.pending[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
		return nil, fmt.Errorf("control write: %w", err)
	}
	return waiter, nil
}

// Close detaches the client and does not return until the process is gone.
//
// Waiting is the whole point. A control client is a process that outlives the
// tmux server it attached to, so one that is merely asked to leave and not
// waited for is invisible to every accounting anybody has: not in
// list-clients, not collectable by kill-server, only in ps. Closing stdin is
// the polite ask; tmux exits on EOF. One that has not exited within a bounded
// wait is killed, and one that survives the kill is reported rather than
// waited on forever, so this can never hang a caller either.
//
// Every close writes a line, so open and close reconcile from the log.
func (c *Control) Close() error {
	c.mu.Lock()
	closed := c.closed
	c.closed = true
	c.mu.Unlock()
	if closed {
		// The read loop already ended -- server gone, or a previous Close.
		// The reap still has to run, and closeOnce makes that safe to repeat.
		c.reap("already-closed")
		return nil
	}
	c.stdin.Close()
	timeout := time.NewTimer(c.replyTimeout())
	defer timeout.Stop()
	select {
	case <-c.done:
		c.reap("eof")
		return nil
	case <-timeout.C:
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	killed := time.NewTimer(c.replyTimeout())
	defer killed.Stop()
	select {
	case <-c.done:
		c.reap("killed")
		return nil
	case <-killed.C:
	}
	c.abandon()
	return fmt.Errorf("control client on %q did not exit after SIGKILL", c.socket)
}

// reap runs the close bookkeeping exactly once: collect the process, log the
// close, and hand back the socket's client slot and the pool entry.
func (c *Control) reap(reason string) {
	c.closeOnce.Do(func() {
		// Wait only once the read loop has ended. done closing means stdout
		// reached EOF, so the process is on its way out and Wait collects it
		// rather than blocking; calling it on a client that survived SIGKILL
		// would hang here forever and take Close's bound with it.
		if c.cmd != nil && exited(c.done) {
			c.cmd.Wait()
		}
		logging.Info("tmux control close",
			"socket", c.socket, "session", c.session, "reason", reason)
		if c.onClose != nil {
			c.onClose()
		}
	})
}

// abandon is the close that failed: a client still running after SIGKILL.
//
// It consumes closeOnce, so the socket's slot is deliberately never given
// back. A process that is still attached is still a client of that server
// whatever this program thinks, and the ceiling has to count it. The manager
// then runs one client poorer on that socket and degrades to the capture
// path, which is the direction to fail in.
func (c *Control) abandon() {
	c.closeOnce.Do(func() {
		logging.Warn("tmux control abandoned",
			"socket", c.socket, "session", c.session,
			"pid", c.pid(), "cap", maxControlClientsPerSocket)
	})
}

// pid names the process a human has to go and kill by hand, since nothing
// else can see it: it has no server to be listed by.
func (c *Control) pid() int {
	if c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// exited reports whether a done channel has closed, without waiting.
func exited(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (c *Control) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	// capture-pane of a colored 200-column pane produces lines far past
	// bufio's 64KB default.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var block []string
	inBlock := false
	blockTag := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case !inBlock && strings.HasPrefix(line, "%begin "):
			inBlock = true
			blockTag = blockID(line)
			block = block[:0]
		case inBlock && blockEnd(line, blockTag):
			c.resolve(strings.Join(block, "\n"), strings.HasPrefix(line, "%error "))
			inBlock = false
		case inBlock:
			// Everything else inside a block is command output verbatim -
			// including pane text that happens to start with %begin or
			// %end. Only the terminator carrying this block's own tag ends
			// it; anything less desyncs every reply after this one.
			block = append(block, line)
		}
		// Notifications carry nothing any caller needs: the one client left
		// attaches with no-output, and %session-changed, %layout-change and
		// %exit told only the mirror things it no longer exists to hear.
	}
	c.fail(scanner.Err())
}

// blockID is the "<timestamp> <number>" pair tmux stamps on %begin and
// repeats on the matching %end/%error.
func blockID(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return ""
	}
	return fields[1] + " " + fields[2]
}

// blockEnd reports whether line terminates the block tagged tag.
func blockEnd(line, tag string) bool {
	if !strings.HasPrefix(line, "%end ") && !strings.HasPrefix(line, "%error ") {
		return false
	}
	return blockID(line) == tag
}

// resolve hands a finished reply block to the oldest waiter. tmux emits one
// unsolicited block when the client connects, before it reads any command,
// so the first block is always the greeting and never resolves a waiter.
func (c *Control) resolve(text string, isError bool) {
	c.mu.Lock()
	if !c.greeted {
		c.greeted = true
		c.mu.Unlock()
		return
	}
	var waiter chan reply
	if len(c.pending) > 0 {
		waiter = c.pending[0]
		c.pending = c.pending[1:]
	}
	c.mu.Unlock()
	if waiter == nil {
		return
	}
	if isError {
		waiter <- reply{err: fmt.Errorf("tmux control: %s", text)}
		return
	}
	waiter <- reply{text: text}
}

// fail ends the client: records the read error, wakes every waiter via
// done, and marks the client closed so new commands are refused.
func (c *Control) fail(err error) {
	c.mu.Lock()
	c.closed = true
	c.exitErr = err
	c.pending = nil
	c.mu.Unlock()
	close(c.done)
}
