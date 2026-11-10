package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
)

// orderFleet is a question, an urgent finish carrying the cache warden's
// deadline, an ordinary finish and an idle row, each last turned to at a
// different time, so every order puts them in a different line.
func orderFleet(t *testing.T, order string) *Model {
	t.Helper()
	urgent := smartSess("urgent-done", status.Finished, 30*time.Minute)
	urgent.Priority = priority.Urgent
	m := smartFleet(t,
		smartSess("ask", status.Waiting, time.Hour),
		urgent,
		smartSess("done", status.Finished, 5*time.Hour),
		smartSess("napping", status.Idle, 3*time.Hour),
	)
	m.queueDeadlines = map[string]time.Time{"urgent-done": smartNow.Add(50 * time.Minute)}
	m.triageOrder = order
	m.rebuildRows()
	return m
}

func TestTriageOrders(t *testing.T) {
	for _, tc := range []struct {
		order       string
		rail, drain []string
	}{
		{triageOrderQuestions, []string{"ask", "urgent-done", "done", "napping"}, []string{"ask", "urgent-done", "done", "napping"}},
		{"", []string{"ask", "urgent-done", "done", "napping"}, []string{"ask", "urgent-done", "done", "napping"}},
		// Priority beats state: the urgent finish goes ahead of the question.
		{triageOrderPriority, []string{"urgent-done", "ask", "done", "napping"}, []string{"urgent-done", "ask", "done", "napping"}},
		// Longest since the operator's last turn first, whatever the state,
		// priority or deadline.
		{triageOrderOldest, []string{"done", "napping", "ask", "urgent-done"}, []string{"done", "napping", "ask", "urgent-done"}},
		// The rail sorts as questions first; the drain hands over only the
		// question.
		{triageOrderQuestionsOnly, []string{"ask", "urgent-done", "done", "napping"}, []string{"ask"}},
	} {
		t.Run(tc.order, func(t *testing.T) {
			m := orderFleet(t, tc.order)
			assertRail(t, m, tc.rail...)
			if got := drainOrder(m); !slices.Equal(got, tc.drain) {
				t.Fatalf("drain = %v, want %v", got, tc.drain)
			}
		})
	}
}

// Oldest first is a fair rotation: with the warm sessions re-finishing on
// every hop, every session is still handed over once before any comes back.
func TestTriageOldestFirstRotatesEverySession(t *testing.T) {
	m, asks, warm := rotationFleet(t, 6)
	m.triageOrder = triageOrderOldest
	m.triageKeepAlives = true
	m.rebuildRows()
	handed := handOverAll(t, m, warm, len(asks)+len(warm)+2)
	if !sameSet(handed, append(slices.Clone(asks), warm...)) {
		t.Fatalf("drain handed over %v, want each of %v and %v once", handed, asks, warm)
	}
}

// Questions only never picks a drain back up for a finish: the session stays
// on the rail, but nothing in it is a question.
func TestTriageQuestionsOnlyPicksUpNoFinish(t *testing.T) {
	m := smartFleet(t, smartSess("done", status.Finished, time.Hour))
	m.triageOrder = triageOrderQuestionsOnly
	m.triageResume = true
	if cmd := m.triagePickupCmd(); cmd != nil {
		t.Fatal("questions only picked a finish back up")
	}
}

// A keep-alive rest stays on the rail but is out of the drain until the
// operator asks for it, and then comes last.
func TestTriageKeepAlivesToggle(t *testing.T) {
	m := smartFleet(t,
		smartSess("kept-warm", status.Finished, time.Minute),
		smartSess("done", status.Finished, 3*time.Hour),
	)
	keepWarm(m, []string{"kept-warm"}, smartNow.Add(time.Second))
	assertRail(t, m, "done", "kept-warm")
	if got := drainOrder(m); !slices.Equal(got, []string{"done"}) {
		t.Fatalf("drain = %v, want the keep-alive rest left out by default", got)
	}
	m.triageKeepAlives = true
	if got := drainOrder(m); !slices.Equal(got, []string{"done", "kept-warm"}) {
		t.Fatalf("drain = %v, want the keep-alive rest last once included", got)
	}
}

func TestTriageOrderSettingsPersist(t *testing.T) {
	m := buildModel(t)
	if m.triageOrder != triageOrderQuestions || m.triageKeepAlives {
		t.Fatalf("defaults = %q, %v; want questions first, keep-alives out", m.triageOrder, m.triageKeepAlives)
	}
	m.openSettings()
	m.settings.field = settingsFieldTriageOrder
	m.cycleSetting(1)
	m.cycleSetting(1)
	m.settings.field = settingsFieldTriageKeepAlives
	m.cycleSetting(1)
	if view := ansi.Strip(m.viewSettings()); !strings.Contains(view, "oldest first") {
		t.Fatalf("settings do not show the chosen order: %s", view)
	}
	m.saveAndCloseSettings()
	if m.triageOrder != triageOrderOldest || !m.triageKeepAlives {
		t.Fatalf("live = %q, %v; want oldest first with keep-alives, without a restart", m.triageOrder, m.triageKeepAlives)
	}
	if got := storedTriageOrder(m.store); got != triageOrderOldest {
		t.Fatalf("stored order = %q", got)
	}
	if !storedTriageKeepAlives(m.store) {
		t.Fatal("keep-alives toggle not stored")
	}
	if err := m.store.SetSetting(triageOrderSetting, "nonsense"); err != nil {
		t.Fatal(err)
	}
	if got := storedTriageOrder(m.store); got != triageOrderQuestions {
		t.Fatalf("unknown order read as %q, want questions first", got)
	}
}

// The footer's triage key says which order the drain is walking.
func TestTriageFooterShowsTheOrder(t *testing.T) {
	m := orderFleet(t, triageOrderPriority)
	if legend := ansi.Strip(m.peekLegend(m.listBodyHeight())); !strings.Contains(legend, "priority first") {
		t.Fatalf("footer does not name the triage order: %s", legend)
	}
}
