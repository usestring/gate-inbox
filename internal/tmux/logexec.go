package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/tmuxguard"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// Every tmux invocation in this package goes through one of the helpers
// below: a tmux failure's error text names neither the server it went to, nor
// how long it took, nor what tmux printed back, and that is most of what an
// "it did nothing" report needs.

func (d *Driver) combined(full []string) ([]byte, error) {
	tmuxguard.Enforce(full)
	start := time.Now()
	countExec(full)
	out, err := exec.Command(d.bin, full...).CombinedOutput()
	logTmux(full, start, err, out)
	return out, err
}

// ErrTimeout marks a tmux call that was killed by its caller's deadline
// rather than answered. It is the one failure that carries no information
// about tmux's state at all: the server was not reached, so nothing may be
// concluded from what it did not say -- least of all that a session is gone.
// Callers that read absence as evidence check for it first.
var ErrTimeout = errors.New("tmux call timed out")

// combinedWithin is combined with a deadline on it. tmux's server is
// single-threaded, so a command aimed at a busy one queues behind whatever
// that server is already doing and nothing bounds the wait; a caller on a
// clock needs the call to end whether or not tmux got to it.
//
// The deadline is reported as ErrTimeout over an empty output, not as the
// "signal: killed" the kill itself produces, because a killed tmux prints
// nothing and several readers in this package take nothing for an answer.
func (d *Driver) combinedWithin(ctx context.Context, full []string) ([]byte, error) {
	tmuxguard.Enforce(full)
	start := time.Now()
	countExec(full)
	cmd := exec.CommandContext(ctx, d.bin, full...)
	// CombinedOutput collects through a pipe, and Wait does not return while
	// anything still holds the write end -- a killed tmux's own children do.
	// WaitDelay caps that second wait, so the deadline bounds the call rather
	// than only the process.
	cmd.WaitDelay = killGrace
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		err = fmt.Errorf("%w after %s", ErrTimeout, took(start))
		out = nil
	}
	logTmux(full, start, err, out)
	return out, err
}

// killGrace is how long after the kill Wait may still spend waiting on pipes
// a dead command's children left open. Long enough that an ordinary exit is
// never cut short, short enough that it is noise beside any deadline here.
const killGrace = 100 * time.Millisecond

func (d *Driver) output(full []string) ([]byte, error) {
	tmuxguard.Enforce(full)
	start := time.Now()
	countExec(full)
	out, err := exec.Command(d.bin, full...).Output()
	// Output keeps stderr off the return value, so tmux's complaint is only
	// reachable through the exit error.
	logTmux(full, start, err, stderrOf(err))
	return out, err
}

func stderrOf(err error) []byte {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.Stderr
	}
	return nil
}

func (d *Driver) silent(full []string) error {
	tmuxguard.Enforce(full)
	start := time.Now()
	err := exec.Command(d.bin, full...).Run()
	logTmux(full, start, err, nil)
	return err
}

// traceTmux records one tmux invocation as a span of its own.
//
// Every helper above funnels through logTmux, so this is the one place that
// sees all of them -- and what a tmux call costs has been the standing blind
// spot: a pass's phase total says a capture took ninety milliseconds without
// saying whether that was one slow server or forty ordinary calls.
//
// The argv is left out deliberately. A chained capture's command list runs to
// kilobytes, and carrying it on every span would cost more than the call it
// describes; the log line already has it for the failures that need it.
//
// A server with no session to answer for is recorded as the answer it is and
// not as an error: tmux exits 1 for it, and every reader behind noServer
// takes it for "nothing of mine is here" and carries on, so an ERROR there
// contradicts the code that read it. It contradicts it expensively, because
// the tail sampler keeps errored spans unconditionally and samples the rest
// at 1-in-50 -- on a box where managers come and go on private sockets these
// were 50 of one hour's 51 errored list-panes spans, crowding out the one
// real failure among them.
func traceTmux(full []string, start time.Time, err error, out []byte) {
	if !tracing.Enabled() {
		return
	}
	socket, command := splitSocket(full)
	attrs := []tracing.Attr{
		{Key: "socket", Value: socket},
		{Key: "args", Value: len(command)},
	}
	// Only a failure tmux explained this way. A timeout reports no output at
	// all (see combinedWithin), so it can never be mistaken for one of these
	// and stays the error it is.
	if err != nil && noServer(string(out)) {
		attrs = append(attrs, tracing.Attr{Key: "outcome", Value: "no-sessions"})
		err = nil
	}
	tracing.Record("tmux."+execVerb(full), start, time.Now(), err, attrs...)
}

func logTmux(full []string, start time.Time, err error, out []byte) {
	traceTmux(full, start, err, out)
	// The guard asks about the level this call is about to write, not about
	// info: gating both branches on info would make "warn" and "error" --
	// the two settings a user picks to see only failures -- log nothing.
	if err != nil {
		if !logging.Enabled(logging.LevelWarn) {
			return
		}
		socket, command := splitSocket(full)
		logging.Warn("tmux command failed",
			"socket", socket,
			"cmd", strings.Join(command, " "),
			"took", took(start),
			"err", err.Error(),
			"out", firstLine(out))
		return
	}
	// The routine record is debug, not info. Every pass runs tens of these,
	// and on the operator's board they were 98.1% of the log's records and
	// 98.5% of its bytes -- 17.7-23.1 MB/hour, an 8 MiB rotation plus a gzip
	// every 28 minutes. The disk is the smaller half: redaction runs in the
	// caller before the sink sees anything, at 1.3-2.8 MB/s, so a batched
	// capture's 5.7 KB argv spends milliseconds of poll-pass CPU on a line
	// nobody reads. A failure still warns, which is what the log is for, and
	// the span above still measures every call, which is what the cost of one
	// is now read from.
	if !logging.Enabled(logging.LevelDebug) {
		return
	}
	socket, command := splitSocket(full)
	logging.Debug("tmux command",
		"socket", socket,
		"cmd", strings.Join(command, " "),
		"took", took(start))
}

// splitSocket names the server a command went to. tmux takes -L before the
// command word, so the socket is the front of the argument list rather than
// anything the caller passed.
func splitSocket(full []string) (string, []string) {
	if len(full) >= 2 && full[0] == "-L" {
		return full[1], full[2:]
	}
	return "", full
}

func took(start time.Time) string {
	return time.Since(start).Round(time.Microsecond).String()
}

// firstLine keeps a failure's message without dragging a whole pane's worth
// of tmux output into the record.
func firstLine(out []byte) string {
	text := strings.TrimSpace(string(out))
	if line, _, cut := strings.Cut(text, "\n"); cut {
		return line
	}
	return text
}
