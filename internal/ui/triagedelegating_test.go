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

// A delegating parent has ended its turn: what it wrote is the operator's to
// read while its helper works, so the drain hands it over as the finish it
// is -- oldest first -- and it is not what needsPerson calls a question.
func TestTriageHandsOverADelegatingParent(t *testing.T) {
	m := buildModel(t)
	delegatingFleet(t, m, status.Working)

	lead := sessionNamed(t, m, "lead")
	if lead.Status != status.Delegating {
		t.Fatalf("lead = %q, want delegating while its helper works", lead.Status)
	}
	if m.needsPerson(lead) || !m.triageWalkable(lead) {
		t.Fatal("a delegated parent is not walkable, or reads as a question")
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
	if want := []string{"lead", "done", "rest"}; !slices.Equal(handed, want) {
		t.Fatalf("drain handed over %v, want %v", handed, want)
	}
}

// Strict triage queues a delegating parent after idle, ahead of errored and
// working; smart triage ranks it as the finish it is.
func TestTriageQueuesADelegatingParentAsAnEndedTurn(t *testing.T) {
	for _, smart := range []bool{false, true} {
		m := buildModel(t)
		m.smartTriage = smart
		delegatingFleet(t, m, status.Working)
		names := sessionNames(m)
		at := func(name string) int { return slices.Index(names, name) }
		ok := at("done") < at("rest") && at("rest") < at("lead")
		if smart {
			ok = at("lead") < at("done") && at("done") < at("rest")
		}
		if !ok {
			t.Fatalf("smart=%v: queue = %v", smart, names)
		}
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

func TestDelegatingIsOffTheAttentionFilter(t *testing.T) {
	if requiresInput(status.Delegating) || statusFilterAttention.matches(status.Delegating) {
		t.Fatal("a delegated parent is on the attention filter")
	}
}

// The parent comes back to the operator's queue when its helper's report
// has been delivered and the parent has finished the turn it started.
func TestDelegatedParentBecomesActionableWhenCompletionReportLands(t *testing.T) {
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
		// Delegating or finished, the parent's turn has ended and it is on
		// the walk; only the plain finish reads as needing a person.
		if lead.Status != want || m.needsPerson(lead) != delivered || !m.triageWalkable(lead) {
			t.Fatalf("delivered=%v: lead=%+v, want %s, walkable, needing a person once delivered", delivered, lead, want)
		}
	}
}
