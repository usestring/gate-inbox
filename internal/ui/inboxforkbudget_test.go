package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// What makes a slow poll pass slow is the inbox, and what the inbox spent
// was tmux processes. Measured on the operator's board over 2h23m: of the 47
// passes that ran over a second, derive.inbox was the dominant phase in 42,
// median 1.049s against 0.038s for derive.status and 0.046s for the whole
// board's capture. Per queued session the gate forked three tmux calls of
// its own -- a second capture of a pane the pass had just captured, the
// caret, and the session activity stamp -- and each of them queued behind
// every other command on a single-threaded server holding eighty panes,
// where one command has been measured at 885ms.
//
// So this counts the forks rather than asserting the guards that produce
// them, for the reason internal/tmux/execcount.go gives: every one of those
// guards passed on its own while the sum of them went unmeasured.
func TestTheInboxGateForksNothingForAPaneThePassCaptured(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "ready-tool")
	queueMessage(t, m, sess.ID, "rebase on main")
	heads := queuedHeads(t, m)

	// Every send slot taken before a fork is counted. The gate still runs
	// every read it would run, and then finds no slot to claim with, so the
	// paste -- which forks a capture and a send-keys of its own, off the
	// pass and on another goroutine -- never starts and cannot be counted
	// against the gate that did not send it.
	for i := range sendsInFlight {
		if !m.poller.reserveSend(inputSend(fmt.Sprintf("filler%d", i))) {
			t.Fatalf("slot %d refused before the cap", i)
		}
	}

	capture := restingCapture(t, m, sess.ID)
	if !capture.State.Read {
		t.Fatal("the batched capture carried no pane state: the gate has nothing to reuse, " +
			"and both halves below would be measuring the same fallback")
	}

	gateForks := func(capture tmux.Capture) int64 {
		t.Helper()
		tmux.ResetExecCounts()
		if err := m.poller.maybeDeliverInbox(sess, heads, capture, status.Idle, true); err != nil {
			t.Fatalf("maybeDeliverInbox: %v", err)
		}
		t.Logf("gate forked %d tmux processes: %v", tmux.ExecTotal(), tmux.ExecCounts())
		return tmux.ExecTotal()
	}

	// The control, and the figure this change removes. Handed the same pane
	// text with no state attached -- which is what a caller outside the poll
	// pass has -- the gate reads the pane, the caret and the activity stamp
	// itself, a forked tmux each. It is measured here rather than quoted
	// because it is also the proof that the run below reached those reads at
	// all: a gate that returned early, on a dialog or a claim or an empty
	// queue, would fork nothing either and would assert nothing.
	if asked := gateForks(tmux.Capture{Text: capture.Text}); asked != 3 {
		t.Fatalf("a gate with no pane state forked %d tmux processes, want 3 "+
			"(the pane, the caret, the activity stamp)", asked)
	}
	if reused := gateForks(capture); reused != 0 {
		t.Fatalf("the gate forked %d tmux processes for a pane the pass had already read, want none", reused)
	}
	// Neither run typed anything, so nothing the delivery itself costs has
	// been charged to the gate above.
	if queued, err := m.store.QueuedCount(sess.ID); err != nil || queued != 1 {
		t.Fatalf("QueuedCount = %d (%v), want the one message still queued behind the send cap", queued, err)
	}
}

// restingCapture is the pass's own batched read, taken once the tool has
// drawn its prompt. A pane captured mid-launch is a pane the gate holds for
// a different reason than the one under test.
func restingCapture(t *testing.T, m *Model, sessionID string) tmux.Capture {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		capture, listed := m.tmux.CapturePanes([]string{sessionID})[sessionID]
		if !listed || capture.Err != nil {
			t.Fatalf("the pass's capture of %s failed: listed=%v err=%v", sessionID, listed, capture.Err)
		}
		if strings.Contains(capture.Text, "❯") {
			return capture
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never drew its prompt: %q", sessionID, capture.Text)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
