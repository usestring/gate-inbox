package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// agingNow is the pinned clock every aging test reads.
var agingNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// agingFleet is a board that always has somebody waiting: questions and
// finished sessions interleaved by age, one finished session past the
// default threshold and one inside it. extra sessions carry their own
// LastStatusAt.
func agingFleet(t *testing.T, m *Model, after time.Duration, extra ...store.Session) {
	t.Helper()
	m.clock = func() time.Time { return agingNow }
	m.finishedAgeAfter = after
	ago := func(d time.Duration) time.Time { return agingNow.Add(-d) }
	seeds := append([]store.Session{
		{ID: "qold0001", Name: "q-old", Status: status.Waiting, LastStatusAt: ago(40 * time.Minute)},
		{ID: "dage0001", Name: "done-aged", Status: status.Finished, LastStatusAt: ago(30 * time.Minute)},
		{ID: "qyng0001", Name: "q-young", Status: status.Waiting, LastStatusAt: ago(20 * time.Minute)},
		{ID: "dfrs0001", Name: "done-fresh", Status: status.Finished, LastStatusAt: ago(5 * time.Minute)},
		{ID: "qfrs0001", Name: "q-fresh", Status: status.Waiting, LastStatusAt: ago(time.Minute)},
	}, extra...)
	for _, sess := range seeds {
		sess.Tool = "claude"
		sess.Cwd = "/tmp"
		sess.CreatedAt = ago(time.Hour)
		if err := m.store.CreateSession(sess); err != nil {
			t.Fatalf("create session %q: %v", sess.ID, err)
		}
	}
	loadStoredRows(t, m)
	m.triage = true
	m.rebuildRows()
}

// drainOrder walks the whole queue the way successive handovers do.
func drainOrder(m *Model) []string {
	var handed []string
	left := ""
	tried := map[string]bool{}
	for {
		i, ok := m.nextTriageInput(left, tried)
		if !ok {
			return handed
		}
		left = m.rows[i].sess.ID
		tried[left] = true
		handed = append(handed, m.rows[i].sess.Name)
	}
}

func TestTriageAgesAFinishedSessionIntoTheWaitingTier(t *testing.T) {
	m := buildModel(t)
	agingFleet(t, m, defaultFinishedAgeAfter)

	// done-aged has sat finished 30 minutes, so it queues between the
	// question 40 minutes old and the one 20 minutes old; done-fresh is
	// inside the threshold and stays under every question.
	want := []string{"q-old", "done-aged", "q-young", "q-fresh", "done-fresh"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("rail = %v, want %v", got, want)
	}
	if got := drainOrder(m); !slices.Equal(got, want) {
		t.Fatalf("drain = %v, want the rail's order %v", got, want)
	}
}

func TestTriageAgingMovesARowAsTimePasses(t *testing.T) {
	m := buildModel(t)
	agingFleet(t, m, defaultFinishedAgeAfter)

	// Six minutes on, done-fresh has sat eleven: no store event, only the
	// rebuild a poll pass makes. It is younger than every question but
	// q-fresh, which is now seven minutes old.
	m.clock = func() time.Time { return agingNow.Add(6 * time.Minute) }
	m.rebuildRows()
	want := []string{"q-old", "done-aged", "q-young", "done-fresh", "q-fresh"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("rail = %v, want %v", got, want)
	}
}

func TestTriageAgingOffKeepsWaitingFirst(t *testing.T) {
	m := buildModel(t)
	agingFleet(t, m, 0)

	want := []string{"q-old", "q-young", "q-fresh", "done-aged", "done-fresh"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("rail = %v, want %v", got, want)
	}
	if got := drainOrder(m); !slices.Equal(got, want) {
		t.Fatalf("drain = %v, want %v", got, want)
	}
}

func TestTriageAgingLeavesAMutedSessionLast(t *testing.T) {
	m := buildModel(t)
	agingFleet(t, m, defaultFinishedAgeAfter)
	for i := range m.sessions {
		if m.sessions[i].Name == "done-aged" {
			m.sessions[i].Muted = true
		}
	}
	m.rebuildRows()

	names := sessionNames(m)
	if names[len(names)-1] != "done-aged" {
		t.Fatalf("rail = %v, want the muted done-aged last", names)
	}
	if got := drainOrder(m); slices.Contains(got, "done-aged") {
		t.Fatalf("drain = %v handed over a muted session", got)
	}
}

// A working parent whose child has sat finished past the threshold is lifted
// to the waiting tier and queues on the child's wait, not its own.
func TestTriageAgedFinishedChildLiftsItsParent(t *testing.T) {
	m := buildModel(t)
	agingFleet(t, m, defaultFinishedAgeAfter,
		store.Session{ID: "lead0001", Name: "lead", Status: status.Working, LastStatusAt: agingNow.Add(-2 * time.Hour)},
		store.Session{ID: "kid00001", Name: "kid", ParentID: "lead0001", SpawnedBy: "lead0001", Status: status.Finished, LastStatusAt: agingNow.Add(-25 * time.Minute)},
	)

	names := sessionNames(m)
	at := func(name string) int { return slices.Index(names, name) }
	if !(at("done-aged") < at("lead") && at("lead") < at("q-young")) {
		t.Fatalf("rail = %v, want lead lifted between done-aged (30m) and q-young (20m) by its 25m child", names)
	}
	if at("kid") != at("lead")+1 {
		t.Fatalf("rail = %v, want kid drawn under its lead", names)
	}

	// Inside the threshold the same child is a fresh finish, and the lead
	// rises only to the finished tier, under every question.
	m.finishedAgeAfter = time.Hour
	m.rebuildRows()
	names = sessionNames(m)
	if at("q-fresh") > at("lead") {
		t.Fatalf("rail = %v, want an un-aged child to leave the lead under the questions", names)
	}
}

func TestStoredFinishedAgeAfter(t *testing.T) {
	m := buildModel(t)
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{"", defaultFinishedAgeAfter},
		{"0", 0},
		{"0s", 0},
		{"30m", 30 * time.Minute},
		{"soon", defaultFinishedAgeAfter},
		{"-5m", defaultFinishedAgeAfter},
	} {
		if err := m.store.SetSetting(triageFinishedAgeSetting, tc.raw); err != nil {
			t.Fatalf("set %q: %v", tc.raw, err)
		}
		if got := storedFinishedAgeAfter(m.store); got != tc.want {
			t.Fatalf("stored %q = %v, want %v", tc.raw, got, tc.want)
		}
	}
}
