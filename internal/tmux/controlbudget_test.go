package tmux

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

// Opens and closes have to reconcile from the log alone.
//
// The 2026-08-27 outage was diagnosed by counting lines: 584 "tmux control
// open" and not one close, on the operator's own sessions. The count was
// only available because the open was logged; the leak was invisible because
// the close never was. A client that opens and is not reported closing is
// indistinguishable in the log from one still in use, which is why nobody
// saw this until the server was already gone.
func TestControlOpensAndClosesReconcileInTheLog(t *testing.T) {
	path := logToTemp(t)
	driver := requireTmux(t)
	const rounds = 4
	for i := 0; i < rounds; i++ {
		control, err := driver.OpenPollControl(testSocket)
		if err != nil {
			t.Fatalf("OpenPollControl: %v", err)
		}
		if err := control.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	driver.CloseCaptureClients()
	body := readLog(t, path)
	opens := strings.Count(body, "tmux control open")
	closes := strings.Count(body, "tmux control close")
	if opens == 0 {
		t.Fatal("no control client was opened, so this proves nothing")
	}
	if opens != closes {
		t.Fatalf("%d control clients opened and %d closed; an open that never reports a close "+
			"is exactly what hid the leak that killed the operator's tmux server", opens, closes)
	}
}

// The backstop. Reuse and the adopted-pane refusal are what keep the client
// count at two or three, but they are code, and code has bugs; the outage was
// one. So the manager must be structurally unable to exceed a small number of
// clients on any server, and must say so loudly when it tries.
func TestControlClientsAreCappedPerSocket(t *testing.T) {
	path := logToTemp(t)
	driver := requireTmux(t)
	// Every client is held open at once, which is the only way to reach the
	// ceiling. OpenPollControl deduplicates nothing -- the reuse lives a
	// layer up, in captureClients -- so calling it in a loop is exactly the
	// runaway caller the cap exists to survive.
	var held []*Control
	t.Cleanup(func() {
		for _, control := range held {
			control.Close()
		}
		driver.CloseCaptureClients()
	})
	// A fixed bound, not maxControlClientsPerSocket+n: the assertion is that
	// a small number of clients is where the refusal lands, and a test that
	// counted up to whatever the constant says would keep passing if the
	// constant were raised to a number that is no ceiling at all.
	const attempts = 12
	if maxControlClientsPerSocket >= attempts {
		t.Fatalf("the cap is %d, which is not a small bounded number", maxControlClientsPerSocket)
	}
	var capped error
	for i := 0; i < attempts; i++ {
		control, err := driver.OpenPollControl(testSocket)
		if err != nil {
			capped = err
			break
		}
		held = append(held, control)
		if got := driver.ControlClientsOn(testSocket); got > maxControlClientsPerSocket {
			t.Fatalf("the manager holds %d control clients on one server, cap is %d",
				got, maxControlClientsPerSocket)
		}
	}
	if capped == nil {
		t.Fatalf("opened %d control clients on one tmux server with no refusal; the cap is not enforced", len(held))
	}
	if !errors.Is(capped, ErrControlAtCap) {
		t.Fatalf("refusal at the cap: want ErrControlAtCap, got %v", capped)
	}
	if len(held) != maxControlClientsPerSocket {
		t.Fatalf("the refusal landed after %d clients, not at the cap of %d",
			len(held), maxControlClientsPerSocket)
	}
	if body := readLog(t, path); !strings.Contains(body, "tmux control clients at cap") {
		t.Fatal("hitting the cap wrote nothing to the log; a silent ceiling is a ceiling nobody finds")
	}
}

// TestMirrorIsReusedForTheSameSession lived here until 2026-08-27. It pinned
// the mirror pool: selecting the same session twice had to reuse one client
// rather than fork a second. The mirror is gone entirely, and the reuse that
// replaced it is per server rather than per session. Captures no longer use
// it at all -- they fork per pane, so tmux cannot retain their replies
// (tmux/tmux#5553) -- and TestCapturePanesReadsEveryServerByForkingPerPane
// pins that; the pooled client now carries only send-keys and PaneState.

// Close does not return until the process is gone. A control client outlives
// the tmux server it attached to, so one that is asked to leave and not
// waited for is invisible to list-clients and uncollectable by kill-server --
// only ps can see it, which is how 238 of them accumulated in the test suite
// before anybody noticed.
func TestCloseWaitsForTheClientToExit(t *testing.T) {
	driver := requireTmux(t)
	t.Cleanup(driver.closeAnchors)
	control, err := driver.OpenPollControl(testSocket)
	if err != nil {
		t.Fatalf("OpenPollControl: %v", err)
	}
	if control.cmd == nil || control.cmd.Process == nil {
		t.Fatal("the control client has no process to wait for")
	}
	pid := control.cmd.Process.Pid
	if err := control.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Wait has already run inside Close, so the process is gone rather than
	// a zombie: signal 0 to a reaped pid cannot succeed.
	if processAlive(pid) {
		t.Fatalf("Close returned while the control client (pid %d) was still running", pid)
	}
	if got := driver.ControlClientsOn(testSocket); got != 0 {
		t.Fatalf("Close left %d clients on the socket's budget", got)
	}
	// Closing twice is what a pooled client sees at shutdown, and must be
	// harmless rather than a second wait or a double release.
	if err := control.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := driver.ControlClientsOn(testSocket); got != 0 {
		t.Fatalf("a second Close moved the budget to %d", got)
	}
}

// processAlive reports whether a pid names a live, unreaped process. Signal 0
// checks for permission to signal and delivers nothing; ESRCH is the answer
// that the process is gone, and a zombie still answers nil -- which is why
// Close has to Wait rather than merely kill.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
