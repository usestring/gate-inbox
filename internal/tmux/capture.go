package tmux

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
)

// captureTimeout bounds one server's whole capture batch. The pass runs on
// the poll cadence, so an unreachable server has to cost a beat rather than
// the interval it would take to answer.
const captureTimeout = 1500 * time.Millisecond

// captureBackoff holds a server off after its client failed. Without it a
// wedged server pays captureTimeout on every pass, forever.
const captureBackoff = 15 * time.Second

// Capture is one pane's read. An Err is silence and never evidence: the
// pane may be perfectly alive on a server that did not answer, and a caller
// that reads it as "gone" wipes a board on one bad tmux call.
type Capture struct {
	Text  string
	Err   error
	State CaptureState
	// At is when the read was asked for, so the pane it shows is no older
	// than this.
	At time.Time
}

// CaptureState is what tmux said about a pane inside the capture's own
// command list: where the caret sits in the text that came back with it,
// and when a client last typed into the session.
//
// It rides on the separator line the chain already prints, so it costs no
// command of its own and no second process. That is the point of it: a
// caller deciding whether it may type into a pane wants three reads -- the
// screen, the caret and the activity stamp -- and paying a forked tmux for
// each of them, per session, on every poll pass, is what made a slow pass
// slow once the captures themselves were batched.
//
// Reading them together is the more correct answer as well as the cheaper
// one. A caret is an index into rows, and a caret fetched a moment after
// the rows it indexes can land on a row that has since scrolled. What
// separates them here is one server running the next command in a list it
// is already holding, rather than two more processes queueing behind
// everything else the board asked of it.
type CaptureState struct {
	CursorX, CursorY int
	// InputAt is when a client attached to the session last sent it a
	// keystroke, and the zero time when none has -- the same value, read the
	// same way, that SessionInputAt reports.
	InputAt time.Time
	// Read is false where the chain carried no state at all: the scrollback
	// sweep asks for none, and a display-message tmux would not expand
	// answers with nothing. A caller that needs the state has to ask for it
	// itself then, which is what every caller did before this existed.
	Read bool
}

// captureStateFormat is what each pane is asked for beside its text. The
// created stamp travels with the activity one because that pair is what
// tells a real keystroke from a session that has never had one; see
// keystrokeAt.
const captureStateFormat = "#{cursor_x},#{cursor_y},#{session_created},#{session_activity}"

// captureClients holds one control-mode client per tmux server. Its clients
// live as long as their servers do: opening costs a process and an attach,
// and the whole point is that the pass after this one pays neither.
type captureClients struct {
	mu      sync.Mutex
	clients map[string]*Control
	state   map[string]serverState
}

// serverState is a server being skipped. exec marks the servers whose
// client could not be opened at all, where forking is the only way left to
// read a pane; the rest are servers that stopped answering, which must not
// then be hit with one fork per pane.
type serverState struct {
	until time.Time
	exec  bool
}

// CapturePanes reads every session's visible pane, one control-mode client
// per tmux server rather than one process per pane. Sessions whose server
// could not be read come back with an Err and no text.
func (d *Driver) CapturePanes(ids []string) map[string]Capture {
	out := make(map[string]Capture, len(ids))
	byServer := map[string][]string{}
	var order []string
	for _, id := range ids {
		socket := d.TargetFor(id).Socket
		if _, seen := byServer[socket]; !seen {
			order = append(order, socket)
		}
		byServer[socket] = append(byServer[socket], id)
	}
	for _, socket := range order {
		d.captureServer(socket, byServer[socket], out)
	}
	return out
}

// CaptureScrollback reads a scrollback window from panes named by pane id,
// forking one tmux per pane.
//
// It exists beside CapturePanes because the naming sweep does not hold session
// ids: it walks adopted panes, which it knows only as a socket and a "%id", and
// it wants history rather than the visible screen. It reads them the same way
// a poll pass reads a board -- one forked command list per server, the panes
// chained inside it -- so a sweep costs a process rather than one per pane.
// capture-pane never runs over the pooled control client. tmux buffers a
// control client's command replies with no ceiling -- the flow control in
// control.c meters pushed %output blocks only, and command replies take an
// unmetered path (tmux/tmux#5553, open and unfixed through 3.7c). On a
// long-lived server the effect is a leak: a 36-pane board polled every two
// seconds grew this box's server ~33 GiB/day until it held 40 GiB.
//
// Measured here: 7.39 kB retained per capture over the pipe against 0.01 kB
// forked, and 36 forked captures -- one whole poll pass -- cost 91ms, under
// 5% of the interval. display-message and send-keys over the same client
// retain nothing and still take the pipe; it is capture-pane's replies,
// which carry a screen of text each, that the server holds on to.
//
// This paragraph used to say the bytes were freed to glibc and only the
// arena was never returned, which would have made the leak a matter of
// allocator tuning. TestCaptureRetentionOverThePipe says otherwise, and it
// is worth reading before anyone tries to put captures back on the pipe:
//
//   - Growth is exactly linear over 40,000 captures, 8048 kB per 5000 every
//     interval, with no plateau. Fragmentation plateaus; this does not.
//   - Starting the server with MALLOC_TRIM_THRESHOLD_, MALLOC_MMAP_THRESHOLD_
//     and MALLOC_ARENA_MAX changes it by 0.06%. The probe reads the server's
//     own /proc environ back, so that is a real null and not a setting that
//     failed to arrive.
//   - Retention scales with the size of the reply: a pane four times as tall
//     retains 6.73 kB per capture against 1.62 kB, within 4% of proportional.
//
// So the bytes are not freed at all, and the number is a property of the
// pane rather than a constant -- 6.73 kB at 200 rows is where the 7.39 kB
// above comes from. Nothing in an allocator knob or in tmux 3.8 addresses
// it: 3.8 bounds replies buffered for a control client that has stopped
// reading (tmux/tmux#5565, the PR for #5553), and this client reads
// continuously, so that bound never engages for it.
func (d *Driver) CaptureScrollback(socket string, paneIDs []string, lines int) map[string]Capture {
	out := make(map[string]Capture, len(paneIDs))
	if len(paneIDs) == 0 {
		return out
	}
	start := time.Now()
	failed := 0
	window := []string{}
	if lines > 0 {
		window = []string{"-S", "-" + strconv.Itoa(lines)}
	}
	for remaining := paneIDs; len(remaining) > 0; {
		batch := remaining
		if len(batch) > captureChainMax {
			batch = batch[:captureChainMax]
		}
		asked := time.Now()
		texts, _, err := d.chainCapture(socket, batch, false, window...)
		for i, text := range texts {
			out[batch[i]] = Capture{Text: text, At: asked}
		}
		if len(texts) == len(batch) {
			remaining = remaining[len(batch):]
			continue
		}
		// The pane the list stopped on takes the error; the rest follow in
		// the next chain, so one closed pane cannot cost a sweep its board.
		out[batch[len(texts)]] = Capture{Err: err}
		failed++
		remaining = remaining[len(texts)+1:]
	}
	logCaptureBatch(socket, len(paneIDs), failed, start, nil)
	return out
}

func (d *Driver) captureScrollbackByExec(socket, pane string, lines int) (string, error) {
	args := []string{"capture-pane", "-p"}
	if lines > 0 {
		args = append(args, "-S", "-"+strconv.Itoa(lines))
	}
	args = append(args, "-t", pane)
	full := append([]string{"-L", socket}, args...)
	countExec(full)
	out, err := exec.Command(d.bin, full...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (d *Driver) captureServer(socket string, ids []string, out map[string]Capture) {
	start := time.Now()
	failed := 0
	for remaining := ids; len(remaining) > 0; {
		batch := remaining
		if len(batch) > captureChainMax {
			batch = batch[:captureChainMax]
		}
		asked := time.Now()
		texts, states, err := d.captureChain(socket, batch)
		for i, text := range texts {
			out[batch[i]] = Capture{Text: text, State: states[i], At: asked}
		}
		if len(texts) == len(batch) {
			remaining = remaining[len(batch):]
			continue
		}
		// tmux runs a command list until one fails and stops there, so the
		// pane after the last separator is the one that could not be read.
		// It takes the error; the panes behind it are re-chained, because a
		// dead pane must not cost its whole server a pass.
		out[batch[len(texts)]] = Capture{Err: err}
		failed++
		remaining = remaining[len(texts)+1:]
	}
	logCaptureBatch(socket, len(ids), failed, start, nil)
}

// captureChainMax bounds one forked command list. The cost of a capture is
// the process, not the read, so the whole point is to put as many panes in
// one fork as possible -- but the reply is a screen of text per pane and the
// argument list a line per pane, and neither wants to be unbounded.
//
// The list is the side with a hard edge: tmux runs none of a command list it
// finds too long, so a chain over the ceiling costs a server its whole pass
// rather than a pane. A pane with state asked for costs about 145 bytes of
// argument list against a target named gi_ plus eight hex, so a full chain is
// near 9 kB -- inside the budget splitCommandList measures against, and well
// inside the 15 kB tmux 3.4 was measured accepting. Anything else that wants
// a per-pane format has to be weighed against that headroom.
const captureChainMax = 64

// captureChain reads several panes on one server with one forked tmux, the
// captures separated by a display-message the panes cannot forge.
//
// This is what makes a poll pass cheap. A fork costs about three
// milliseconds whatever it is asked to do, and the board pays one per pane:
// measured on this box, 40 panes cost 141ms one fork each against 8ms
// chained, for text that is byte for byte the same.
//
// It stays a fork on purpose. The pooled control client is faster still and
// carries everything that retains nothing -- send-keys, display-message --
// but a capture reply is a screen of text that the server never gives back.
// See the note above CaptureScrollback: that is the leak this indirection
// exists to avoid, and chaining pays neither price.
func (d *Driver) captureChain(socket string, ids []string) ([]string, []CaptureState, error) {
	targets := make([]string, len(ids))
	for i, id := range ids {
		targets[i] = d.TargetName(id)
	}
	return d.chainCapture(socket, targets, true, "-e")
}

// chainCapture is the chain itself, over targets rather than session ids, so
// the naming sweep -- which knows its panes only as "%id" and wants a
// scrollback window rather than the visible screen -- reads them the same way
// the poll pass reads a board.
//
// state asks each pane for its caret and its session's activity stamp on the
// separator line, which the poll pass wants and the sweep has no use for.
func (d *Driver) chainCapture(socket string, targets []string, state bool, flags ...string) ([]string, []CaptureState, error) {
	sep, err := captureSeparator()
	if err != nil {
		return nil, nil, err
	}
	args := make([]string, 0, 2+len(targets)*(11+len(flags)))
	args = append(args, "-L", socket)
	for i, target := range targets {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, "capture-pane", "-p")
		args = append(args, flags...)
		args = append(args, "-t", target, ";", "display-message", "-p")
		if !state {
			args = append(args, sep)
			continue
		}
		// -t only where there is a format that needs a pane to expand
		// against. A bare separator is a constant, so the chain the sweep
		// sends stays byte for byte the command list it always sent.
		args = append(args, "-t", target, sep+" "+captureStateFormat)
	}
	// Stdout only: a failing command list writes its complaint to stderr, and
	// mixing that into the last pane's text would put tmux's words on the
	// operator's screen as though the agent had printed them.
	out, runErr := d.output(args)
	texts, states := splitCaptures(string(out), sep, len(targets))
	if runErr != nil {
		runErr = fmt.Errorf("tmux capture-pane on %s: %w: %s", socket, runErr, strings.TrimSpace(string(stderrOf(runErr))))
	} else if len(texts) < len(targets) {
		runErr = fmt.Errorf("tmux capture-pane on %s: %d of %d panes answered", socket, len(texts), len(targets))
	}
	return texts, states, runErr
}

// splitCaptures cuts a command list's output into one block per pane. A
// block runs up to its separator, and whatever follows the separator on that
// same line is the state tmux expanded for that pane.
//
// It stops at want blocks and at the first missing separator, because a list
// tmux stopped early prints no separator for the pane that failed: the tail
// after the last separator is the pane that could not be read, which names
// no pane of its own and is the caller's to report.
func splitCaptures(out, sep string, want int) ([]string, []CaptureState) {
	texts := make([]string, 0, want)
	states := make([]CaptureState, 0, want)
	for len(texts) < want {
		at := strings.Index(out, sep)
		if at < 0 {
			break
		}
		texts = append(texts, out[:at])
		out = out[at+len(sep):]
		line := out
		if end := strings.Index(out, "\n"); end >= 0 {
			line, out = out[:end], out[end+1:]
		} else {
			out = ""
		}
		states = append(states, parseCaptureState(line))
	}
	return texts, states
}

// parseCaptureState reads captureStateFormat back. Anything it cannot parse
// is no state rather than a guess: the caller falls back to asking tmux
// itself, where a made-up caret would decide against the operator's typing.
func parseCaptureState(line string) CaptureState {
	fields := strings.Split(strings.TrimSpace(line), ",")
	if len(fields) != 4 {
		return CaptureState{}
	}
	x, xErr := strconv.Atoi(fields[0])
	y, yErr := strconv.Atoi(fields[1])
	if xErr != nil || yErr != nil {
		return CaptureState{}
	}
	at, err := keystrokeAt(fields[2], fields[3])
	if err != nil {
		return CaptureState{}
	}
	return CaptureState{CursorX: x, CursorY: y, InputAt: at, Read: true}
}

// captureSeparator is a fresh delimiter per batch, so no pane can print the
// line that would split its own capture in two.
func captureSeparator() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("capture separator: %w", err)
	}
	return "gate-inbox-capture-" + hex.EncodeToString(raw[:]), nil
}

func logCaptureBatch(socket string, panes, failed int, start time.Time, fatal error) {
	// Each branch asks about the level it writes, so a log turned down to
	// warn still reports the failures it was turned down to isolate.
	if fatal != nil || failed > 0 {
		if !logging.Enabled(logging.LevelWarn) {
			return
		}
		logging.Warn("tmux capture batch",
			"socket", socket, "panes", panes, "failed", failed,
			"took", time.Since(start).Round(time.Microsecond).String(), logging.Err(fatal))
		return
	}
	if !logging.Enabled(logging.LevelDebug) {
		return
	}
	logging.Debug("tmux capture batch",
		"socket", socket, "panes", panes,
		"took", time.Since(start).Round(time.Microsecond).String())
}

// errNoPooledClient reports that this server has no usable control client,
// so a caller wanting one falls back to forking. Captures no longer ask --
// they always fork -- so today this only turns pipeFor's answer into "no
// client", and it stays a named error because the reason is worth logging.
var errNoPooledClient = errors.New("no pooled control client")

func (c *captureClients) client(d *Driver, socket string) (*Control, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if state, held := c.state[socket]; held {
		if time.Now().Before(state.until) {
			if state.exec {
				return nil, errNoPooledClient
			}
			return nil, fmt.Errorf("tmux server %q is not answering", socket)
		}
		delete(c.state, socket)
	}
	if control, open := c.clients[socket]; open {
		select {
		case <-control.Done():
			// The anchor session was killed, or the server went away.
			// Nothing to salvage; opening again puts the anchor back and
			// attaches to it, which is the whole recovery.
			delete(c.clients, socket)
		default:
			return control, nil
		}
	}
	control, err := d.OpenPollControl(socket)
	if err != nil {
		c.holdLocked(socket, true)
		return nil, errNoPooledClient
	}
	if c.clients == nil {
		c.clients = map[string]*Control{}
	}
	c.clients[socket] = control
	return control, nil
}

// fail drops the client of a server that stopped answering and holds that
// server off. It never marks the server forkable: a live server too slow to
// answer one pipe would be far worse off under one process per pane.
func (c *captureClients) fail(socket string, control *Control) {
	c.mu.Lock()
	if c.clients[socket] == control {
		delete(c.clients, socket)
	}
	c.holdLocked(socket, false)
	c.mu.Unlock()
	// Close waits on the client to go, which is the pass's time to spend
	// on the next server rather than on this one.
	go control.Close()
}

func (c *captureClients) holdLocked(socket string, byExec bool) {
	if c.state == nil {
		c.state = map[string]serverState{}
	}
	c.state[socket] = serverState{until: time.Now().Add(captureBackoff), exec: byExec}
}

// CloseCaptureClients detaches every pooled client and takes down the anchor
// sessions they were attached to. The manager's exit closes their stdin
// anyway, but the anchors outlive that: a session survives the client that
// attached to it, so without this every run would leave one behind on every
// server it read.
func (d *Driver) CloseCaptureClients() {
	d.captures.mu.Lock()
	clients := d.captures.clients
	d.captures.clients = nil
	d.captures.state = nil
	d.captures.mu.Unlock()
	for _, control := range clients {
		control.Close()
	}
	d.closeAnchors()
}

// ExecShape gives a capture read over a control pipe the same bytes the
// forked CapturePane returns: the reply block carries the pane's lines
// without the newline that ends the command's output, and a frame that
// disagreed by one line would shift the whole pane. Exported because every
// capture path has to agree, which two copies of this cannot.
func ExecShape(pane string) string {
	return strings.TrimSuffix(pane, "\n") + "\n"
}
