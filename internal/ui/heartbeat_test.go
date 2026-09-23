package ui

import (
	"strconv"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// beatLatencyBudget is what a whole poll pass may cost while another process
// holds the database and a liveness stamp is due. Generous against heldWrite
// on purpose: the point is the difference between milliseconds and the whole
// write, not a millisecond count a loaded box would flake on.
const beatLatencyBudget = heldWrite / 4

// startBeatWriter runs the liveness writer the way the poll loop does, on its
// own connection, and stops it with the test.
func startBeatWriter(t *testing.T, p *poller) {
	t.Helper()
	writer, err := p.store.Reopen()
	if err != nil {
		t.Fatalf("second connection for the liveness stamps: %v", err)
	}
	done := make(chan struct{})
	go func() {
		p.runBeats(writer)
		writer.Close()
		close(done)
	}()
	t.Cleanup(func() {
		close(p.beats)
		<-done
	})
}

// waitForStamp reads the liveness row until it says something other than
// unchanged, which is the sender-visible half of the guarantee: a manager
// that is polling has to be readable as running.
func waitForStamp(t *testing.T, st *store.Store, unchanged string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := st.Setting(store.PollerHeartbeatKey)
		if err != nil {
			t.Fatal(err)
		}
		if got != unchanged && got != "" {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("no liveness stamp past %q inside five seconds: a manager that is polling reads as closed", unchanged)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stampAge is how old a sender reading the row right now would find it.
func stampAge(t *testing.T, raw string) time.Duration {
	t.Helper()
	nanos, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("liveness stamp %q is not a timestamp: %v", raw, err)
	}
	return time.Since(time.Unix(0, nanos))
}

// A poll pass must never wait on the liveness stamp. The stamp is a write
// transaction against a database some thirty `gate-inbox mcp` processes
// write to as well, and one waiting out another writer was measured at four
// seconds inside a pass -- which is four seconds of a board holding its own
// lock, repainting nothing and delivering nothing.
//
// The pass is timed whole rather than by its phases: handing the write to
// another goroutine on the store's own connection would only move the wait
// one step, into the first query the pass runs behind it, since the store
// keeps a single connection.
func TestAStalledLivenessStampDoesNotStallThePass(t *testing.T) {
	m, dbPath := buildModelWithStorePath(t)
	// The first pass sweeps the inbox; from the second on, admin is the
	// stamp and nothing else.
	m.applyCmd(t, m.refreshCmd())
	startBeatWriter(t, m.poller)

	m.poller.heartbeatAt = time.Now().Add(-store.PollerHeartbeatPeriod - time.Second)
	holdTheWriteLock(t, dbPath)

	started := time.Now()
	var stat passStat
	m.poller.refreshPass(&stat)
	latency := time.Since(started)
	t.Logf("poll pass with a stamp due: %v (another writer holds the database for %v)",
		latency.Round(time.Microsecond), heldWrite)

	if latency > beatLatencyBudget {
		t.Fatalf("a poll pass took %v while another writer held the database for %v: the liveness stamp is still on the pass",
			latency, heldWrite)
	}
	// A pass that feels instant because its work was dropped is not a fix:
	// the stamp still has to land once the lock frees.
	stamped := waitForStamp(t, m.store, "")
	if age := stampAge(t, stamped); age > store.PollerHeartbeatStale {
		t.Fatalf("the stamp that finally landed was already %v old, past the %v its readers allow", age, store.PollerHeartbeatStale)
	}
}

// The heartbeat is what tells a sender whether a manager is home. Stamping it
// on every poll would be a write transaction every couple of seconds for as
// long as the manager stays open, so it is allowed to age instead: its reader
// treats a stamp as fresh for far longer than one poll. What it may never do
// is age past that reader while the manager is still polling.
func TestTheHeartbeatLandsWhileTheManagerPollsAndIsLeftAloneBetweenStamps(t *testing.T) {
	m := buildModel(t)
	startBeatWriter(t, m.poller)

	m.applyCmd(t, m.refreshCmd())
	first := waitForStamp(t, m.store, "")
	if age := stampAge(t, first); age > store.PollerHeartbeatStale {
		t.Fatalf("the first stamp read as %v old, past the %v its readers allow", age, store.PollerHeartbeatStale)
	}

	m.applyCmd(t, m.refreshCmd())
	time.Sleep(100 * time.Millisecond)
	second, err := m.store.Setting(store.PollerHeartbeatKey)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("the poll behind it rewrote the heartbeat: %q then %q", first, second)
	}

	m.poller.heartbeatAt = time.Now().Add(-store.PollerHeartbeatPeriod - time.Second)
	m.applyCmd(t, m.refreshCmd())
	third := waitForStamp(t, m.store, first)
	if age := stampAge(t, third); age > store.PollerHeartbeatStale {
		t.Fatalf("the restamp read as %v old, past the %v its readers allow", age, store.PollerHeartbeatStale)
	}
}

// The row means "a manager is polling, so what you queue will be delivered",
// not "this process is alive". A writer that stamped on a clock of its own
// would keep a wedged or finished poll loop reading as home, and a sender
// would be told its message is on its way to a board that will never type it
// in. So the writer writes only what a pass hands it.
func TestTheLivenessWriterInventsNoStampOfItsOwn(t *testing.T) {
	m := buildModel(t)
	startBeatWriter(t, m.poller)

	// Long enough that a writer running on the poll interval, or on the
	// stamp period scaled to a test, would have written something.
	time.Sleep(500 * time.Millisecond)

	got, err := m.store.Setting(store.PollerHeartbeatKey)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("the writer stamped %q with no pass behind it: a poll loop that has stopped would still read as home", got)
	}
}

// A stamp the writer was too busy to take is dropped rather than waited on,
// and the pass that dropped it must not then behave as though it had
// stamped: the next pass has to try again, or a busy moment costs a whole
// period of liveness.
func TestADroppedStampIsRetriedByTheNextPass(t *testing.T) {
	m := buildModel(t)
	m.applyCmd(t, m.refreshCmd())

	// Nothing is draining beats here, so the first pass's own stamp is
	// still sitting in it and the next send has nowhere to go.
	if len(m.poller.beats) != 1 {
		t.Fatal("the first pass handed over no stamp, so nothing is in the way of the next one")
	}
	aged := time.Now().Add(-store.PollerHeartbeatPeriod - time.Second)
	m.poller.heartbeatAt = aged

	var blocked passStat
	m.poller.refreshPass(&blocked)
	if !m.poller.heartbeatAt.Equal(aged) {
		t.Fatal("a pass that could not hand its stamp over counted it as sent, so nothing restamps for another period")
	}

	<-m.poller.beats
	var retry passStat
	m.poller.refreshPass(&retry)
	select {
	case <-m.poller.beats:
	default:
		t.Fatal("the pass behind the dropped stamp did not try again")
	}
}
