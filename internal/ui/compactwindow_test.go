package ui

import (
	"os"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/store"
)

func compactWindowOf(t *testing.T, m *Model, id string) int {
	t.Helper()
	window, err := m.hooks.SessionCompactWindow(id)
	if err != nil {
		t.Fatal(err)
	}
	return window
}

func storedRow(t *testing.T, m *Model, id string) store.Session {
	t.Helper()
	sess, err := m.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// compactClock puts the poller's compact pass on a clock the test moves.
func compactClock(m *Model) *time.Time {
	now := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	m.poller.compactClock = func() time.Time { return now }
	return &now
}

func compactWindowTools(delay time.Duration) map[string]config.Tool {
	return map[string]config.Tool{"claude": {
		StatusSource:          hooks.StatusSourceClaude,
		CompactWindow:         map[string]int{"urgent": 600000, "high": 300000},
		CompactDowngradeDelay: config.Duration{Duration: delay},
	}}
}

// Every road a tier changes by -- the p key's store write, a declaration
// through the priority command or MCP tool, a group's tier above the
// session, filing it into or out of that group -- reaches the session's own
// settings file: a larger window on the next pass, a smaller one once the
// downgrade delay has run.
func TestPriorityChangeRewritesTheCompactWindow(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.poller.compactTools = compactWindowTools(10 * time.Minute)
	now := compactClock(m)
	const id = "s1" // in group alpha
	sync := func(groups map[string]priority.Tier) {
		m.poller.syncCompactWindows([]store.Session{storedRow(t, m, id)}, groups)
	}
	settle := func(groups map[string]priority.Tier) {
		sync(groups)
		*now = now.Add(10 * time.Minute)
		sync(groups)
	}

	sync(nil)
	if _, err := os.Stat(m.hooks.SessionSettingsPath(id)); !os.IsNotExist(err) {
		t.Fatalf("an untiered session was given its own file: %v", err)
	}

	// The p key writes the store.
	if err := m.store.SetPriority(id, priority.Urgent); err != nil {
		t.Fatal(err)
	}
	sync(nil)
	if got := compactWindowOf(t, m, id); got != 600000 {
		t.Fatalf("urgent window = %d, want 600000", got)
	}
	if got := m.hooks.LaunchSettingsPath(id); got != m.hooks.SessionSettingsPath(id) {
		t.Fatalf("the next launch carries %s, want the session's own file", got)
	}

	// The priority command and set_priority leave a declaration the pass
	// applies.
	declarePriority(t, m, id, "high")
	sess := storedRow(t, m, id)
	if err := m.poller.applyPendingPriority(&sess); err != nil {
		t.Fatal(err)
	}
	settle(nil)
	if got := compactWindowOf(t, m, id); got != 300000 {
		t.Fatalf("high window = %d, want 300000", got)
	}

	// Medium has no window: the file goes and the session is back on the
	// shared one.
	if err := m.store.SetPriority(id, priority.Medium); err != nil {
		t.Fatal(err)
	}
	settle(nil)
	if got := m.hooks.LaunchSettingsPath(id); got != m.hooks.SettingsPath() {
		t.Fatalf("a medium session launches on %s, want the shared file", got)
	}

	// A group tier above it counts, the way the board sorts it.
	urgentBeta := map[string]priority.Tier{"beta": priority.Urgent}
	sync(urgentBeta)
	if got := compactWindowOf(t, m, id); got != 0 {
		t.Fatalf("window outside the urgent group = %d, want 0", got)
	}
	if err := m.store.MoveSession(id, "beta"); err != nil {
		t.Fatal(err)
	}
	sync(urgentBeta)
	if got := compactWindowOf(t, m, id); got != 600000 {
		t.Fatalf("window filed into an urgent group = %d, want 600000", got)
	}
	if err := m.store.MoveSession(id, "alpha"); err != nil {
		t.Fatal(err)
	}
	settle(urgentBeta)
	if got := compactWindowOf(t, m, id); got != 0 {
		t.Fatalf("window filed out of the urgent group = %d, want 0", got)
	}
}

// A smaller window waits out the delay, survives a board restart with the
// time it began, and a tier put back up before it runs out drops it.
func TestCompactDowngradeWaitsOutItsDelay(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.poller.compactTools = compactWindowTools(10 * time.Minute)
	now := compactClock(m)
	const id = "s1"
	sync := func() {
		m.poller.syncCompactWindows([]store.Session{storedRow(t, m, id)}, nil)
	}
	setTier := func(tier priority.Tier) {
		if err := m.store.SetPriority(id, tier); err != nil {
			t.Fatal(err)
		}
	}

	setTier(priority.Urgent)
	sync()

	setTier(priority.High)
	sync()
	if got := compactWindowOf(t, m, id); got != 600000 {
		t.Fatalf("window right after a downgrade = %d, want 600000 kept", got)
	}
	view := m.poller.compactViews[id]
	if view.PendingTo != 300000 || !view.PendingAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("pending downgrade = %+v, want 300000 at +10m", view)
	}

	// Put back up inside the delay: nothing is written and the wait goes.
	*now = now.Add(5 * time.Minute)
	setTier(priority.Urgent)
	sync()
	*now = now.Add(10 * time.Minute)
	sync()
	if got := compactWindowOf(t, m, id); got != 600000 {
		t.Fatalf("window after the tier came back = %d, want 600000", got)
	}
	if m.poller.compactViews[id].Pending() {
		t.Fatal("a downgrade the tier took back is still pending")
	}

	// Lowered again, with the board restarted partway through: the wait
	// keeps the time it began.
	setTier(priority.High)
	sync()
	m.poller.compactPending = nil
	*now = now.Add(9 * time.Minute)
	sync()
	if got := compactWindowOf(t, m, id); got != 600000 {
		t.Fatalf("window 9m into the wait after a restart = %d, want 600000", got)
	}
	m.poller.compactPending = nil
	*now = now.Add(time.Minute)
	sync()
	if got := compactWindowOf(t, m, id); got != 300000 {
		t.Fatalf("window once the wait ran out = %d, want 300000", got)
	}
	if m.poller.compactViews[id].Pending() {
		t.Fatal("a written downgrade is still pending")
	}
}

// With no compact_window configured the pass touches nothing.
func TestCompactWindowOffWritesNothing(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	if err := m.store.SetPriority("s1", priority.Urgent); err != nil {
		t.Fatal(err)
	}
	m.poller.syncCompactWindows([]store.Session{storedRow(t, m, "s1")}, nil)
	if _, err := os.Stat(m.hooks.SessionSettingsPath("s1")); !os.IsNotExist(err) {
		t.Fatalf("a board without compact_window wrote a settings file: %v", err)
	}
}

// A child takes the window its tracker's tier earns, through as many
// spawners as there are, while a detached spawn and a session that tracks
// itself round a cycle keep their own.
func TestChildTakesItsParentsCompactWindow(t *testing.T) {
	m := buildModel(t)
	m.poller.compactTools = compactWindowTools(10 * time.Minute)
	now := compactClock(m)
	parent := store.Session{ID: "parent", Tool: "claude", Priority: priority.High}
	child := store.Session{ID: "child", Tool: "claude", ParentID: "parent", SpawnedBy: "parent"}
	grandchild := store.Session{ID: "grand", Tool: "claude", ParentID: "parent", SpawnedBy: "child"}
	detached := store.Session{ID: "loose", Tool: "claude", SpawnedBy: "parent"}
	m.poller.syncCompactWindows([]store.Session{parent, child, grandchild, detached}, nil)
	for id, want := range map[string]int{"parent": 300000, "child": 300000, "grand": 300000, "loose": 0} {
		if got := compactWindowOf(t, m, id); got != want {
			t.Fatalf("%s window = %d, want %d", id, got, want)
		}
	}

	// A parent raised raises its child on the next pass, and lowered,
	// lowers it once the delay has run.
	parent.Priority = priority.Urgent
	m.poller.syncCompactWindows([]store.Session{parent, child, grandchild}, nil)
	if got := compactWindowOf(t, m, "child"); got != 600000 {
		t.Fatalf("child of an urgent parent window = %d, want 600000", got)
	}
	parent.Priority = priority.High
	m.poller.syncCompactWindows([]store.Session{parent, child, grandchild}, nil)
	*now = now.Add(10 * time.Minute)
	m.poller.syncCompactWindows([]store.Session{parent, child, grandchild}, nil)
	if got := compactWindowOf(t, m, "child"); got != 300000 {
		t.Fatalf("child of a lowered parent window = %d, want 300000", got)
	}

	// The child's own higher tier still wins over the parent's.
	child.Priority = priority.Urgent
	m.poller.syncCompactWindows([]store.Session{parent, child, grandchild}, nil)
	if got := compactWindowOf(t, m, "grand"); got != 600000 {
		t.Fatalf("grandchild of an urgent child window = %d, want 600000", got)
	}

	a := store.Session{ID: "a", Tool: "claude", ParentID: "b", SpawnedBy: "b"}
	b := store.Session{ID: "b", Tool: "claude", ParentID: "a", SpawnedBy: "a"}
	m.poller.syncCompactWindows([]store.Session{a, b}, nil)
	if got := compactWindowOf(t, m, "a"); got != 0 {
		t.Fatalf("an untiered cycle window = %d, want 0", got)
	}
}

// The rail says a window waits for a restart only while the process runs on
// another one, and counts a pending downgrade down against the clock it is
// painted at.
func TestCompactFactFollowsTheLaunchAndTheClock(t *testing.T) {
	now := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		view compactView
		want string
	}{
		{compactView{Window: 600000, Launched: 600000, LaunchKnown: true}, "600k"},
		{compactView{Window: 600000, Launched: 300000, LaunchKnown: true}, "300k → 600k at restart"},
		{compactView{Window: 600000, Launched: 600000, LaunchKnown: true, PendingTo: 300000, PendingAt: now.Add(6*time.Minute + time.Second)}, "600k → 300k in 7m at restart"},
		{compactView{Window: 600000, PendingTo: 0, PendingAt: now.Add(30 * time.Second)}, "600k → default in 30s at restart"},
	} {
		if got := compactFact(c.view, now); got != c.want {
			t.Errorf("compactFact(%+v) = %q, want %q", c.view, got, c.want)
		}
	}
}
