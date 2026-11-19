package ui

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A child with an active parent stays delegated, even after the grace period.
func TestTriageAdvanceLeavesChildrenWithTheirActiveParent(t *testing.T) {
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

func TestTriageHandsOrphanedChildrenOverBeforeLowPriorityIdle(t *testing.T) {
	for _, smart := range []bool{false, true} {
		for _, parentState := range []string{"missing", status.Dead, "archived"} {
			t.Run(fmt.Sprintf("smart=%v/parent=%s", smart, parentState), func(t *testing.T) {
				m := buildModel(t)
				m.cfg.Tools = map[string]config.Tool{"claude": {}}
				m.triage, m.smartTriage = true, smart
				m.sessions = []store.Session{
					childSess("wait", "question", "", "parent", status.Waiting, time.Minute),
					childSess("idle", "later", "", "", status.Idle, time.Minute),
				}
				m.sessions[1].Priority = priority.Low
				for i := range 4 {
					id := fmt.Sprintf("done%d", i)
					m.sessions = append(m.sessions, childSess(id, id, "", "parent", status.Finished, time.Hour))
				}
				if parentState != "missing" {
					parent := childSess("parent", "lead", "", "", status.Dead, time.Hour)
					parent.Archived = parentState == "archived"
					if parent.Archived {
						parent.Status = status.Finished
					}
					m.sessions = append(m.sessions, parent)
				}
				for i := range m.sessions {
					at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
					m.sessions[i].CreatedAt, m.sessions[i].LastStatusAt = at, at
				}
				m.rebuildRows()

				var handed []string
				tried := map[string]bool{}
				for {
					i, ok := m.nextTriageInput("", tried)
					if !ok {
						break
					}
					id := m.rows[i].sess.ID
					handed = append(handed, id)
					tried[id] = true
				}
				want := []string{"wait", "done0", "done1", "done2", "done3", "idle"}
				if !slices.Equal(handed, want) {
					t.Fatalf("handover order = %v, want %v", handed, want)
				}
			})
		}
	}
}
