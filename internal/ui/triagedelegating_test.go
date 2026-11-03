package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// delegatingFleet is a board with a parent at rest on a fan-out beside
// sessions that really are done, laid over the way the poller lays it.
func delegatingFleet(t *testing.T, m *Model, helper string) {
	t.Helper()
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	for i, sess := range []store.Session{
		// The lead has waited longest, so on its own pane's reading it would
		// head the finished tier.
		{ID: "lead0001", Name: "lead", Status: status.Finished},
		{ID: "help0001", Name: "helper", ParentID: "lead0001", SpawnedBy: "lead0001", Status: helper},
		{ID: "done0001", Name: "done", Status: status.Finished},
		{ID: "rest0001", Name: "rest", Status: status.Idle},
	} {
		sess.Tool = "claude"
		sess.Cwd = "/tmp"
		sess.CreatedAt = base
		sess.LastStatusAt = base.Add(time.Duration(i) * time.Minute)
		if err := m.store.CreateSession(sess); err != nil {
			t.Fatalf("create session %q: %v", sess.ID, err)
		}
	}
	loadStoredRows(t, m)
	shown, err := m.poller.delegated(m.sessions)
	if err != nil {
		t.Fatalf("delegated: %v", err)
	}
	m.sessions = shown
	m.triage = true
	m.rebuildRows()
}

func TestTriageIncludesADelegatingParent(t *testing.T) {
	m := buildModel(t)
	delegatingFleet(t, m, status.Working)

	lead := sessionNamed(t, m, "lead")
	if lead.Status != status.Delegating {
		t.Fatalf("lead = %q, want delegating while its helper works", lead.Status)
	}
	if !m.needsPerson(lead) || !m.triageWalkable(lead) {
		t.Fatal("a delegated parent is missing from the operator's queue")
	}
	var handed []string
	left := ""
	tried := map[string]bool{}
	for {
		i, ok := m.nextTriageInput(left, tried)
		if !ok {
			break
		}
		left = m.rows[i].sess.ID
		tried[left] = true
		handed = append(handed, m.rows[i].sess.Name)
	}
	if want := []string{"done", "lead", "rest"}; !slices.Equal(handed, want) {
		t.Fatalf("drain handed over %v, want %v", handed, want)
	}
}

func TestTriageQueuesADelegatingParentBeforeIdle(t *testing.T) {
	m := buildModel(t)
	delegatingFleet(t, m, status.Working)
	names := sessionNames(m)
	at := func(name string) int { return slices.Index(names, name) }
	if !(at("done") < at("lead") && at("lead") < at("rest")) {
		t.Fatalf("queue = %v, want done, the delegated lead, then rest", names)
	}
}

// A child asking a question outranks the parent's own delegated state.
func TestTriageLiftsADelegatingParentWhoseChildAsks(t *testing.T) {
	m := buildModel(t)
	delegatingFleet(t, m, status.Waiting)

	if got := sessionNamed(t, m, "lead").Status; got != status.Delegating {
		t.Fatalf("lead = %q, want delegating while its helper asks", got)
	}
	names := sessionNames(m)
	if len(names) == 0 || names[0] != "lead" {
		t.Fatalf("queue = %v, want the lead lifted to the head by its asking child", names)
	}
}

func TestTriageHandsTheParentOverOnceItsChildrenAreDone(t *testing.T) {
	m := buildModel(t)
	delegatingFleet(t, m, status.Finished)

	lead := sessionNamed(t, m, "lead")
	if lead.Status != status.Finished {
		t.Fatalf("lead = %q, want finished once its helper is done", lead.Status)
	}
	if !m.triageWalkable(lead) {
		t.Fatal("a finished parent with nothing outstanding is off the queue")
	}
}

func TestDelegatingIsOnTheAttentionFilter(t *testing.T) {
	if !requiresInput(status.Delegating) || !statusFilterAttention.matches(status.Delegating) {
		t.Fatal("a delegated parent is missing from the attention filter")
	}
}

func TestDelegatedParentStaysOnAttentionWithoutItsWorkingChild(t *testing.T) {
	m := buildModel(t)
	delegatingFleet(t, m, status.Working)
	m.statusFilter = statusFilterAttention
	m.rebuildRows()
	if got := sessionNames(m); !slices.Equal(got, []string{"done", "lead"}) {
		t.Fatalf("attention = %v, want done and lead", got)
	}
	lead := sessionNamed(t, m, "lead")
	lead.Muted = true
	if m.needsPerson(lead) || m.triageWalkable(lead) {
		t.Fatal("muting a delegated parent did not remove it from triage")
	}
}

func TestDelegatedParentRemainsActionableUntilCompletionReportLands(t *testing.T) {
	m := buildModel(t)
	delegatingFleet(t, m, status.Working)
	child := sessionNamed(t, m, "helper")
	if err := m.store.UpdateStatus(child.ID, status.Finished); err != nil {
		t.Fatal(err)
	}
	if err := m.poller.relayChildRest(child, status.Finished); err != nil {
		t.Fatal(err)
	}
	report, found, err := m.store.HeadMessage("lead0001")
	if err != nil || !found {
		t.Fatalf("child completion report = %+v, found=%v, err=%v", report, found, err)
	}
	for _, delivered := range []bool{false, true} {
		if delivered {
			if err := m.store.MarkDelivered(report.ID, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		loadStoredRows(t, m)
		shown, err := m.poller.delegated(m.sessions)
		if err != nil {
			t.Fatal(err)
		}
		m.sessions = shown
		m.rebuildRows()
		lead := sessionNamed(t, m, "lead")
		want := status.Delegating
		if delivered {
			want = status.Finished
		}
		if lead.Status != want || !m.needsPerson(lead) || !m.triageWalkable(lead) {
			t.Fatalf("delivered=%v: lead=%+v, want actionable %s", delivered, lead, want)
		}
	}
}
