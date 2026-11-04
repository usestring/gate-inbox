package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/singleton"
	"github.com/usestring/gate-inbox/internal/ui"
)

// A dump is the input journal, the key-routing state and every goroutine
// stack, written next to the log. It is what answers "the board stopped
// doing what keys ask" after the board has been restarted.
//
// It is written on request (`gate-inbox dump`, which sends dumpSignal) and on
// its own when the board can tell something is wrong:
//   - the pane it runs in is closed, which is how a stuck board usually ends;
//   - an open key has left the board on the list several times in a row
//     (see the ui package's stuck-open check);
//   - the event loop has not answered a ping for loopStallAfter.
//
// Automatic dumps are rate limited and old dump files are pruned, so a board
// that keeps tripping a check cannot fill the disk.

// dumpSignal asks a running board to write a dump. SIGUSR1 has no other use
// in the manager, and Bubble Tea does not claim it.
const dumpSignal = syscall.SIGUSR1

const (
	// dumpLoopWait is how long a dump waits for the event loop to answer. A
	// loop that does not answer within it is itself the finding.
	dumpLoopWait = 3 * time.Second
	// hangupLoopWait is shorter: the process is about to exit, and whoever
	// closed the pane is not waiting on it, but the next board's start is.
	hangupLoopWait = time.Second
	// autoDumpEvery is the least time between two automatic dumps.
	autoDumpEvery = 10 * time.Minute
	// dumpsKept is how many dump files are kept; the oldest go first.
	dumpsKept = 20
	// loopPingEvery and loopStallAfter pace the event-loop watchdog.
	loopPingEvery  = 5 * time.Second
	loopStallAfter = 20 * time.Second
)

type dumper struct {
	dir  string
	send func(tea.Msg)
	// auto carries automatic triggers to the dump goroutine. Buffered and
	// sent to without blocking: a trigger fired while a dump is under way is
	// dropped, which the rate limit would have done anyway.
	auto chan string

	mu       sync.Mutex
	lastAuto time.Time
}

// startDumper installs the dump signal, the automatic triggers and the loop
// watchdog, and returns the dumper and the func that stops them.
func startDumper(dir string, send func(tea.Msg)) (*dumper, func()) {
	d := &dumper{dir: dir, send: send, auto: make(chan string, 1)}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, dumpSignal)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sig:
				d.write("requested (gate-inbox dump or SIGUSR1)", dumpLoopWait)
			case reason := <-d.auto:
				d.write(reason, dumpLoopWait)
			}
		}
	}()
	go d.watchLoop(done, loopPingEvery, loopStallAfter)
	return d, func() {
		signal.Stop(sig)
		close(done)
	}
}

// Trigger asks for an automatic dump. It never blocks, so the event loop may
// call it, and it is dropped inside the rate limit.
func (d *dumper) Trigger(reason string) {
	if d == nil || !d.allowAuto(time.Now()) {
		return
	}
	select {
	case d.auto <- reason:
	default:
	}
}

func (d *dumper) allowAuto(now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.lastAuto.IsZero() && now.Sub(d.lastAuto) < autoDumpEvery {
		return false
	}
	d.lastAuto = now
	return true
}

// OnHangup writes a dump before the hangup handler exits the process. It
// bypasses the rate limit: it runs once, at the end.
func (d *dumper) OnHangup() {
	d.write("the board's pane was closed (SIGHUP)", hangupLoopWait)
}

func (d *dumper) write(reason string, wait time.Duration) {
	path, err := writeDump(d.dir, reason, d.send, wait)
	if err != nil {
		logging.Warn("dump failed", "reason", reason, logging.Err(err))
		return
	}
	logging.Info("dump written", "reason", reason, "path", path)
	pruneDumps(d.dir, dumpsKept)
}

// watchLoop pings the event loop and dumps when a ping goes unanswered for
// stall. Only one ping is in flight at a time: Send blocks while the loop is
// wedged, and stacking pings behind it would only grow the queue.
func (d *dumper) watchLoop(done <-chan struct{}, every, stall time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
		}
		reply := make(chan struct{}, 1)
		sent := time.Now()
		go d.send(ui.LoopPing{Reply: reply})
		select {
		case <-done:
			return
		case <-reply:
			continue
		case <-time.After(stall):
		}
		d.Trigger(fmt.Sprintf("the event loop did not answer a ping for %s", stall))
		// Wait the stall out before pinging again, so one wedge is one dump
		// and one log line, however long it lasts.
		select {
		case <-done:
			return
		case <-reply:
			logging.Warn("event loop stalled", "for", time.Since(sent).Round(time.Millisecond).String())
		}
	}
}

// writeDump writes one dump file. The board's state is asked for first and
// the goroutine stacks are taken while the loop answers, so a loop that never
// answers still leaves its stacks behind.
func writeDump(dir, reason string, send func(tea.Msg), wait time.Duration) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	path := filepath.Join(dir, "dump-"+now.Format("20060102T150405.000Z")+".txt")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fmt.Fprintf(f, "gate-inbox dump, pid %d, %s\nreason: %s\n\n", os.Getpid(), now.Format(time.RFC3339Nano), reason)

	reply := make(chan string, 1)
	go send(ui.DumpRequest{Reply: reply})

	var stacks bytes.Buffer
	if p := pprof.Lookup("goroutine"); p != nil {
		_ = p.WriteTo(&stacks, 2)
	}

	select {
	case state := <-reply:
		io.WriteString(f, state)
	case <-time.After(wait):
		fmt.Fprintf(f, "== board state: the event loop did not answer within %s; it is blocked, see the stacks below\n", wait)
	}
	fmt.Fprintf(f, "\n== goroutines\n")
	_, err = f.Write(stacks.Bytes())
	return path, err
}

// pruneDumps keeps the newest keep dump files in dir. The names sort by time.
func pruneDumps(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var dumps []string
	for _, entry := range entries {
		if name := entry.Name(); !entry.IsDir() && strings.HasPrefix(name, "dump-") && strings.HasSuffix(name, ".txt") {
			dumps = append(dumps, name)
		}
	}
	if len(dumps) <= keep {
		return
	}
	sort.Strings(dumps)
	for _, name := range dumps[:len(dumps)-keep] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// runDump is `gate-inbox dump`: it signals the board that holds configDir's
// lock and reports where the dump will land.
func runDump(w io.Writer, configDir, logDir string) error {
	pid, err := singleton.Holder(configDir)
	if errors.Is(err, singleton.ErrNoHolder) {
		return errors.New("no board is running for " + configDir)
	}
	if err != nil {
		return fmt.Errorf("%w (send SIGUSR1 to the board yourself to dump it)", err)
	}
	if err := syscall.Kill(pid, dumpSignal); err != nil {
		return fmt.Errorf("signal board pid %d: %w", pid, err)
	}
	fmt.Fprintf(w, "asked board pid %d for a dump; it writes %s within a few seconds\n",
		pid, filepath.Join(logDir, "dump-<time>.txt"))
	return nil
}
