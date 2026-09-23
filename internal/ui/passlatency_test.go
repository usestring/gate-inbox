package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// heldPass is how long the stand-in pass keeps the poller's lock. Long
// enough that a loop waiting on it is unmistakable, short enough to pay for
// on every run.
const heldPass = 2 * time.Second

// latencyBudget is what a poll result plus a keypress plus a frame may cost
// while a pass is in flight. Generous against heldPass on purpose: the point
// is the difference between milliseconds and the whole pass, not a
// millisecond count a loaded CI box would flake on.
const latencyBudget = heldPass / 4

// A pass that has gone slow must cost the operator stale rows, never a frozen
// interface. The result of the previous pass, the keypress behind it and the
// frame they paint all land on the Bubble Tea event loop, and none of them may
// wait on the lock the pass in flight is holding: on a thirty-session board a
// pass has taken as long as thirty seconds, and every key pressed in that
// window was queued behind it.
func TestTheEventLoopDoesNotWaitForAPassInFlight(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "one", t.TempDir(), "")

	// A poll result with geometry to re-assert is the message that used to
	// take the lock; without this the handler has nothing to resize and the
	// test would pass on any code at all.
	result := m.poller.refreshOnce()
	m.pane.geom = nil

	// The poller is read off the model here rather than inside the stand-in:
	// the loop below reassigns the whole model, which rewrites this field.
	poller := m.poller
	holding := make(chan struct{})
	released := make(chan struct{})
	go func() {
		poller.runMu.Lock()
		close(holding)
		time.Sleep(heldPass)
		poller.runMu.Unlock()
		close(released)
	}()
	<-holding

	started := time.Now()
	updated, _ := m.Update(result)
	*m = *updated.(*Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	*m = *updated.(*Model)
	painted := m.frame()
	latency := time.Since(started)
	t.Logf("poll result + keypress + frame: %v (a pass is holding the lock for %v)", latency.Round(time.Microsecond), heldPass)

	if latency > latencyBudget {
		t.Fatalf("poll result, keypress and frame took %v while a pass held the lock for %v", latency, heldPass)
	}
	if painted == "" {
		t.Fatal("no frame was painted")
	}
	<-released
}
