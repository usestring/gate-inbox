package ui

import (
	"os"
	"testing"

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

// Every road a tier changes by -- the p key's store write, a declaration
// through the priority command or MCP tool, a group's tier above the
// session -- rewrites the session's own settings file on the next pass.
func TestPriorityChangeRewritesTheCompactWindow(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.poller.compactTools = map[string]config.Tool{"claude": {
		StatusSource:  hooks.StatusSourceClaude,
		CompactWindow: map[string]int{"urgent": 600000, "high": 300000},
	}}
	const id = "s1" // in group alpha
	sync := func(groups map[string]priority.Tier) {
		m.poller.syncCompactWindows([]store.Session{storedRow(t, m, id)}, groups)
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
	sync(nil)
	if got := compactWindowOf(t, m, id); got != 300000 {
		t.Fatalf("high window = %d, want 300000", got)
	}

	// Medium has no window: the file goes and the session is back on the
	// shared one.
	if err := m.store.SetPriority(id, priority.Medium); err != nil {
		t.Fatal(err)
	}
	sync(nil)
	if got := m.hooks.LaunchSettingsPath(id); got != m.hooks.SettingsPath() {
		t.Fatalf("a medium session launches on %s, want the shared file", got)
	}

	// A group tier above it counts, the way the board sorts it.
	sync(map[string]priority.Tier{"alpha": priority.Urgent})
	if got := compactWindowOf(t, m, id); got != 600000 {
		t.Fatalf("window under an urgent group = %d, want 600000", got)
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
	m.poller.compactTools = map[string]config.Tool{"claude": {
		StatusSource:  hooks.StatusSourceClaude,
		CompactWindow: map[string]int{"urgent": 600000, "high": 300000},
	}}
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
