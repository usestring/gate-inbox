package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// orderFleet is a question, an urgent finish carrying the cache warden's
// deadline, an ordinary finish and an idle row, each last turned to at a
// different time to exercise the tie-breaks within a priority.
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
	if err := m.store.SetSetting(triageOrderSetting, order); err != nil {
		t.Fatal(err)
	}
	m.triageOrder = storedTriageOrder(m.store)
	m.rebuildRows()
	return m
}

func TestTriageDeferralsStayBehindNewWorkUntilReset(t *testing.T) {
	for _, order := range triageOrders {
		t.Run(order, func(t *testing.T) {
			m := orderFleet(t, order)
			before := sessionNames(m)
			m.deferTriageSession("ask")
			m.deferTriageSession("urgent-done")
			// A new turn must not undo the operator's decision to postpone it.
			for i := range m.sessions {
				if m.sessions[i].ID == "ask" {
					m.sessions[i].LastStatusAt = smartNow
				}
			}
			m.sessions = append(m.sessions, smartSess("new-work", status.Waiting, time.Minute))
			m.rebuildRows()
			if rail := sessionNames(m); !slices.Equal(rail[len(rail)-2:], []string{"ask", "urgent-done"}) {
				t.Fatalf("rail = %v, want skipped sessions at the back", rail)
			}
			got := drainOrder(m)
			if order == triageOrderQuestionsOnly {
				if !slices.Equal(got, []string{"new-work", "ask"}) {
					t.Fatalf("questions-only drain = %v, want the deferred question last", got)
				}
			} else if len(got) != 5 || !slices.Equal(got[len(got)-2:], []string{"ask", "urgent-done"}) {
				t.Fatalf("drain = %v, want skipped sessions after all other work", got)
			}
			m.sessions = slices.DeleteFunc(m.sessions, func(sess store.Session) bool { return sess.ID == "new-work" })
			m.clearMutes()
			m.rebuildRows()
			if got := sessionNames(m); !slices.Equal(got, before) {
				t.Fatalf("reset queue = %v, want %v", got, before)
			}
		})
	}
}

func TestTriageOrders(t *testing.T) {
	for _, tc := range []struct {
		order       string
		rail, drain []string
	}{
		{"questions-first", []string{"urgent-done", "ask", "done", "napping"}, []string{"urgent-done", "ask", "done", "napping"}},
		{"", []string{"urgent-done", "ask", "done", "napping"}, []string{"urgent-done", "ask", "done", "napping"}},
		{triageOrderPriority, []string{"urgent-done", "ask", "done", "napping"}, []string{"urgent-done", "ask", "done", "napping"}},
		{triageOrderOldest, []string{"urgent-done", "done", "napping", "ask"}, []string{"urgent-done", "done", "napping", "ask"}},
		{triageOrderQuestionsOnly, []string{"urgent-done", "ask", "done", "napping"}, []string{"ask"}},
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

// Within one priority, with warm sessions re-finishing on
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
	m.triageKeepAlives = false
	if got := drainOrder(m); !slices.Equal(got, []string{"done"}) {
		t.Fatalf("drain = %v, want the keep-alive rest left out when off", got)
	}
	m.triageKeepAlives = true
	if got := drainOrder(m); !slices.Equal(got, []string{"done", "kept-warm"}) {
		t.Fatalf("drain = %v, want the keep-alive rest last once included", got)
	}
}

// The reported board: an idle session the cache warden kept warm after the
// operator's last turn must still be handed over, after the plain idle one.
// A store from before the default flip carries "off" that saveSettings wrote
// without anybody choosing it; the flip must override it once.
func TestTriageWalksKeptWarmIdleSessionsByDefault(t *testing.T) {
	m := buildModel(t)
	for key, value := range map[string]string{triageKeepAlivesSetting: "off", triageKeepAlivesDefaultSetting: ""} {
		if err := m.store.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if !storedTriageKeepAlives(m.store) {
		t.Fatal("a pre-flip stored off kept keep-alive rests out of the drain")
	}
	if got, _ := m.store.Setting(triageKeepAlivesSetting); got != "on" {
		t.Fatalf("flip stored %q, want on", got)
	}

	fleet := smartFleet(t,
		smartSess("kept-warm-idle", status.Idle, 10*time.Minute),
		smartSess("napping", status.Idle, 5*time.Hour),
	)
	fleet.triageKeepAlives = storedTriageKeepAlives(m.store)
	keepWarm(fleet, nil, smartNow)
	fleet.choreRests = map[string]time.Time{"kept-warm-idle": smartNow.Add(-time.Minute)}
	fleet.rebuildRows()
	if got := drainOrder(fleet); !slices.Equal(got, []string{"napping", "kept-warm-idle"}) {
		t.Fatalf("drain = %v, want both idle sessions, the kept-warm one last", got)
	}
}

func TestTriageOrderSettingsPersist(t *testing.T) {
	m := buildModel(t)
	if m.triageOrder != triageOrderPriority || !m.triageKeepAlives {
		t.Fatalf("defaults = %q, %v; want priority first, keep-alives in", m.triageOrder, m.triageKeepAlives)
	}
	// Toggle off then back on, so the saved value is one the operator chose.
	m.openSettings()
	m.settings.field = settingsFieldTriageKeepAlives
	m.cycleSetting(1)
	m.saveAndCloseSettings()
	if m.triageKeepAlives || storedTriageKeepAlives(m.store) {
		t.Fatal("an explicit off after the default flip did not stick")
	}
	m.openSettings()
	m.settings.field = settingsFieldTriageOrder
	m.cycleSetting(1)
	m.settings.field = settingsFieldTriageKeepAlives
	m.cycleSetting(1)
	if view := ansi.Strip(m.viewSettings()); !strings.Contains(view, "priority then oldest") {
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
	if got := storedTriageOrder(m.store); got != triageOrderPriority {
		t.Fatalf("unknown order read as %q, want priority first", got)
	}
}

// The footer's triage key says which order the drain is walking.
func TestTriageFooterShowsTheOrder(t *testing.T) {
	m := orderFleet(t, triageOrderPriority)
	if legend := ansi.Strip(m.peekLegend(m.listBodyHeight())); !strings.Contains(legend, "priority then questions") {
		t.Fatalf("footer does not name the triage order: %s", legend)
	}
}
