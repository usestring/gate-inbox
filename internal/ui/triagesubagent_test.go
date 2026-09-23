package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The drain hands over the session that spawned the work, never the pane
// under it. A child stranded on a question past the grace period lifts its
// parent up the queue; the walk then lands on the parent, and the child --
// whatever its status -- is stepped over.
func TestTriageAdvanceNeverFocusesASubagent(t *testing.T) {
	m := buildModel(t)
	m.cfg.Tools = map[string]config.Tool{"claude": {}}
	m.triage = true
	m.sessions = []store.Session{
		childSess("p1", "worker", "research", "", status.Working, time.Hour),
		childSess("c1", "probe", "research", "p1", status.Waiting, ownedGrace+time.Minute),
		childSess("s9", "unrelated", "research", "", status.Idle, 20*time.Minute),
	}
	m.rebuildRows()

	seen := map[string]bool{}
	tried := map[string]bool{}
	for {
		i, ok := m.nextTriageInput("", tried)
		if !ok {
			break
		}
		id := m.rows[i].sess.ID
		seen[id], tried[id] = true, true
	}
	if seen["c1"] {
		t.Fatal("the drain offered the subagent's pane")
	}
	if !seen["s9"] {
		t.Fatal("the top-level idle session was not offered")
	}
}

func TestIsSubagentIsAChildWithAParent(t *testing.T) {
	if !isSubagent(store.Session{ParentID: "run"}) {
		t.Fatal("a child with a parent is not a subagent")
	}
	if isSubagent(store.Session{}) {
		t.Fatal("a top-level session read as a subagent")
	}
}
