package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const stillPrompt = "all done\n\n❯ \n"

// stalePass folds one reading into p the way a poll pass does: the stale set
// is rebuilt, then the session's reading is noted.
func stalePass(p *poller, sess store.Session, derived, screen string, at time.Time) bool {
	p.stale = map[string]bool{}
	p.noteScreen(sess, derived, screen, at)
	return p.staleRows()[sess.ID]
}

// The case the flag exists for: a pane sitting at a prompt that the rules
// still read as working. Nothing on the screen moves, so once the label has
// held past the threshold the row is flagged.
func TestAWorkingLabelOverAStillScreenIsFlaggedStale(t *testing.T) {
	for _, label := range []string{status.Working, status.Starting} {
		p := &poller{staleAfter: time.Hour}
		sess := store.Session{ID: "s", Status: label}
		start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

		if stalePass(p, sess, label, stillPrompt, start) {
			t.Fatalf("%s: flagged on the first look", label)
		}
		if stalePass(p, sess, label, stillPrompt, start.Add(59*time.Minute)) {
			t.Fatalf("%s: flagged before the threshold", label)
		}
		if !stalePass(p, sess, label, stillPrompt, start.Add(31*time.Hour)) {
			t.Fatalf("%s: a label held over an unchanged screen for 31h was not flagged stale", label)
		}
	}
}

// A long turn that is really running paints, and a screen that changes is
// never flagged however long the label has held.
func TestABusyScreenIsNeverFlaggedStale(t *testing.T) {
	p := &poller{staleAfter: time.Hour}
	sess := store.Session{ID: "s", Status: status.Working}
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	for i := range 40 {
		screen := strings.Repeat("line\n", i) + "✻ Working\n"
		if stalePass(p, sess, status.Working, screen, start.Add(time.Duration(i)*time.Hour)) {
			t.Fatalf("pass %d: a screen that changes every pass was flagged stale", i)
		}
	}
}

// A status change is a change, and starts the clock again.
func TestAStatusChangeRestartsTheStaleClock(t *testing.T) {
	p := &poller{staleAfter: time.Hour}
	sess := store.Session{ID: "s", Status: status.Working}
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	stalePass(p, sess, status.Working, stillPrompt, start)
	stalePass(p, sess, status.Starting, stillPrompt, start.Add(50*time.Minute))
	if stalePass(p, sess, status.Starting, stillPrompt, start.Add(90*time.Minute)) {
		t.Fatal("the time spent under the previous label counted toward the new one")
	}
	if !stalePass(p, sess, status.Starting, stillPrompt, start.Add(111*time.Minute)) {
		t.Fatal("the new label was not flagged once it had held past the threshold itself")
	}
}

// A resting label over a still screen is what resting looks like.
func TestARestingLabelIsNeverFlaggedStale(t *testing.T) {
	for _, label := range []string{status.Idle, status.Finished, status.Waiting, status.Errored, status.Dead} {
		p := &poller{staleAfter: time.Hour}
		sess := store.Session{ID: "s", Status: label}
		start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
		stalePass(p, sess, label, stillPrompt, start)
		if stalePass(p, sess, label, stillPrompt, start.Add(48*time.Hour)) {
			t.Fatalf("%s over a still screen was flagged stale", label)
		}
	}
}

// Unset, the threshold falls back to the default rather than to zero, which
// would flag every working row on its second pass.
func TestTheStaleThresholdDefaultsWhenUnset(t *testing.T) {
	p := &poller{}
	sess := store.Session{ID: "s", Status: status.Working}
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	stalePass(p, sess, status.Working, stillPrompt, start)
	if stalePass(p, sess, status.Working, stillPrompt, start.Add(defaultStaleStatusAfter-time.Minute)) {
		t.Fatal("flagged before the default threshold")
	}
	if !stalePass(p, sess, status.Working, stillPrompt, start.Add(defaultStaleStatusAfter)) {
		t.Fatal("not flagged at the default threshold")
	}
}

// The row says so, and triage offers it up: a working row is otherwise the
// one thing triage never walks into, which is exactly how a misread label
// went unnoticed.
func TestAStaleRowIsMarkedAndWalkedByTriage(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"stuck": status.Working, "busy": status.Working})
	m.triage = true
	m.rebuildRows()
	stuck := sessionNamed(t, m, "stuck")
	busy := sessionNamed(t, m, "busy")

	if m.triageWalkable(stuck) || strings.Contains(triageRail(m), "stale") {
		t.Fatal("a working row was walkable or marked stale before anything flagged it")
	}

	m.stale = map[string]bool{stuck.ID: true}
	m.rebuildRows()
	if !m.triageWalkable(stuck) {
		t.Error("a stale working row is not offered up by triage")
	}
	if m.triageWalkable(busy) {
		t.Error("a working row that is not stale became walkable")
	}
	if m.triageRankOf(stuck) != triageRank(status.Idle) {
		t.Errorf("stale rank = %d, want the idle tier %d", m.triageRankOf(stuck), triageRank(status.Idle))
	}
	rail := triageRail(m)
	var stuckLine, busyLine bool
	for _, line := range strings.Split(rail, "\n") {
		if strings.Contains(line, "stale") {
			stuckLine = true
		}
	}
	busyLine = strings.Count(rail, "stale") > 1
	if !stuckLine {
		t.Errorf("the stale row carries no mark:\n%s", rail)
	}
	if busyLine {
		t.Errorf("more than one row is marked stale:\n%s", rail)
	}
}
