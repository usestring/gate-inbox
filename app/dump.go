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
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/singleton"
	"github.com/usestring/gate-inbox/internal/ui"
)

// dumpSignal asks a running board to write a dump. SIGUSR1 has no other use
// in the manager, and Bubble Tea does not claim it.
const dumpSignal = syscall.SIGUSR1

// dumpLoopWait is how long a dump waits for the event loop to answer. A loop
// that does not answer within it is itself the finding: the board is wedged
// inside a handler, and the goroutine stacks above say which.
const dumpLoopWait = 3 * time.Second

// dumpOnSignal writes a dump next to the log each time the board is sent
// dumpSignal, and returns the func that takes the handler down again.
//
// It is for the board that has stopped doing what keys ask while the operator
// is still looking at it: run `gate-inbox dump` from any shell before
// restarting it, and the file says whether keys were arriving, which branch
// took them, and what the event loop was doing.
func dumpOnSignal(logDir string, send func(tea.Msg)) func() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, dumpSignal)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sig:
				path, err := writeDump(logDir, send, dumpLoopWait)
				if err != nil {
					logging.Warn("dump failed", logging.Err(err))
					continue
				}
				logging.Info("dump written", "path", path)
			}
		}
	}()
	return func() {
		signal.Stop(sig)
		close(done)
	}
}

// writeDump writes the goroutine stacks first, from this goroutine, so they
// land even when the event loop never answers; then asks the loop for its
// journal and state.
func writeDump(dir string, send func(tea.Msg), wait time.Duration) (string, error) {
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
	fmt.Fprintf(f, "gate-inbox dump, pid %d, %s\n\n", os.Getpid(), now.Format(time.RFC3339Nano))

	// Sent before the stacks are taken, so a loop that is merely busy has
	// the time it takes to write them to get to the request.
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
