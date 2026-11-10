package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func sessionID(t *testing.T, m *Model, name string) string {
	t.Helper()
	for _, sess := range m.sessions {
		if sess.Name == name {
			return sess.ID
		}
	}
	t.Fatalf("no session named %q", name)
	return ""
}

// Among sessions ready for input, a raised tier goes ahead of lower tiers,
// after the questions: an urgent finished session and an urgent idle one both
// go before every other untiered row, but behind an untiered question. The
// rail has to read in the order the drain walks.
func TestPriorityLiftsAheadOfLowerTiers(t *testing.T) {
	m := buildModel(t)
	m.smartTriage = false // the strict order; smart triage has triagesmart_test.go
	seedTriageFleet(t, m)
	if err := m.store.SetPriority(sessionID(t, m, "reviewme"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "napping"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))

	want := []string{"old-block", "new-block", "reviewme", "napping", "grinder", "booting", "gone", "crashed"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage order = %v want %v", got, want)
	}
	rail := triageRail(m)
	if !strings.Contains(rail, "reviewme "+priority.Urgent.Glyph()) {
		t.Fatalf("the urgent row wears no glyph:\n%s", rail)
	}
	assertWidelyDrawn(t, "priority rail", rail)
}

// A group's tier covers its subtree, so every session filed under it that
// needs a person goes first, and a session moved out of the group sheds it
// with the group.
func TestGroupPriorityCoversTheSubtree(t *testing.T) {
	m := buildModel(t)
	m.smartTriage = false // the strict order; smart triage has triagesmart_test.go
	seedTriageFleet(t, m)
	if err := m.store.SetGroupPriority("beta", priority.Urgent); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))

	want := []string{"new-block", "old-block", "reviewme", "napping", "grinder", "booting", "gone", "crashed"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage order = %v want %v", got, want)
	}
}

// Raising a failed turn must not put it ahead of work that can move forward.
func TestTriageDrainHandsOverPriorityFirst(t *testing.T) {
	m := buildModel(t)
	m.smartTriage = false // the strict order; smart triage has triagesmart_test.go
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"done":  status.Finished,
		"rest":  status.Idle,
		"spare": status.Idle,
	})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	// Raised while the operator is inside ask, so it is nowhere near the
	// ring's next step; the row order is what the previous build left.
	if err := m.store.SetPriority(sessionID(t, m, "broke"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	for i := range m.sessions {
		if m.sessions[i].Name == "broke" || m.sessions[i].Name == "spare" {
			m.sessions[i].Priority = priority.Urgent
		}
	}
	m.rebuildRows()
	for _, want := range []string{"spare", "done", "rest", "broke"} {
		updated, _ := m.handleFocusKey(ctrlQ())
		m = updated.(*Model)
		if m.mode != modeFocus {
			t.Fatalf("ctrl+q dropped out of the queue before %q: %s", want, m.errBar.text)
		}
		if got := focusedName(t, m); got != want {
			t.Fatalf("ctrl+q landed on %q want %q", got, want)
		}
	}
}

// p walks the whole cycle on the store and the rail together, one press at
// a time, and comes back to no tier at all -- the only way to clear one.
func TestPriorityKeyCyclesTheSession(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	id := sessionID(t, m, "napping")

	for _, want := range []priority.Tier{priority.Urgent, priority.High, priority.Medium, priority.Low, priority.Unset} {
		m.selectSessionRow(t, "napping")
		pressKey(t, m, key("p"))
		got, err := m.store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Priority != want {
			t.Fatalf("p stored %q, want %q", got.Priority, want)
		}
		rail := triageRail(m)
		if want == priority.Unset {
			if strings.Contains(rail, "napping "+priority.Low.Glyph()) {
				t.Fatalf("the glyph outlived the tier:\n%s", rail)
			}
			continue
		}
		if !strings.Contains(rail, "napping "+want.Glyph()) {
			t.Fatalf("%q waited for a poll:\n%s", want, rail)
		}
	}
}

// Ready sessions come first; priority orders each band before state does.
func TestTiersOrderTheQueue(t *testing.T) {
	m := buildModel(t)
	m.smartTriage = false // the strict order; smart triage has triagesmart_test.go
	seedTriageFleet(t, m)
	if err := m.store.SetPriority(sessionID(t, m, "old-block"), priority.Low); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "new-block"), priority.High); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "crashed"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "reviewme"), priority.High); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "grinder"), priority.High); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))

	// A high working session waits below an untiered idle one and a low
	// question, while still leading the other sessions that cannot take input.
	want := []string{"new-block", "reviewme", "napping", "old-block", "grinder", "booting", "gone", "crashed"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage order = %v want %v", got, want)
	}
}

func TestPriorityWorkingWaitsBehindReadySessions(t *testing.T) {
	for _, smart := range []bool{false, true} {
		t.Run(fmt.Sprintf("smart=%t", smart), func(t *testing.T) {
			ask := smartSess("ask", status.Waiting, time.Second)
			ask.Priority = priority.Low
			done := smartSess("done", status.Finished, time.Second)
			busy := smartSess("busy", status.Working, time.Second)
			busy.Priority = priority.Urgent
			starting := smartSess("starting", status.Starting, time.Second)
			starting.Priority = priority.High
			m := smartFleet(t, busy, starting, ask, done)
			m.smartTriage = smart
			m.rebuildRows()

			assertRail(t, m, "done", "ask", "busy", "starting")
			if got, want := drainOrder(m), []string{"done", "ask"}; !slices.Equal(got, want) {
				t.Fatalf("handovers = %v, want %v", got, want)
			}
			m.sessions[0].Status = status.Waiting
			m.rebuildRows()
			assertRail(t, m, "busy", "done", "ask", "starting")
		})
	}
}

// p on a group row tiers the group; root refuses, since it is every
// session and a tier on everything ranks nothing.
func TestPriorityKeyTogglesTheGroupAndRefusesRoot(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectGroupRow(t, "alpha")
	pressKey(t, m, key("p"))
	groups, err := m.store.Groups()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Name == "alpha" && g.Priority != priority.Urgent {
			t.Fatalf("p on the group row stored %q, want urgent", g.Priority)
		}
	}
	rail := triageRail(m)
	if !strings.Contains(rail, "alpha "+priority.Urgent.Glyph()) {
		t.Fatalf("group row wears no glyph:\n%s", rail)
	}
	if !strings.Contains(rail, "old-block "+priority.Urgent.Glyph()) {
		t.Fatalf("session under a tiered group wears no glyph:\n%s", rail)
	}

	m.selectGroupRow(t, rootGroup)
	pressKey(t, m, key("p"))
	if m.errBar.text == "" {
		t.Fatal("p on root said nothing")
	}
}

// A tier ranks a session ahead of every lower tier, but never past a
// question: an agent stopped on a dialog is stalled this second, and an urgent
// session that keeps finishing would otherwise hold it unreached. Turning
// triage on hands over the untiered question first, then the urgent rows.
func TestARaisedTierStaysBehindAQuestion(t *testing.T) {
	m := buildModel(t)
	m.smartTriage = false // the strict order; smart triage has triagesmart_test.go
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"done":  status.Finished,
		"rest":  status.Idle,
	})
	if err := m.store.SetPriority(sessionID(t, m, "done"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "rest"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))

	if got, want := sessionNames(m), []string{"ask", "done", "rest", "broke"}; !slices.Equal(got, want) {
		t.Fatalf("triage order = %v want %v", got, want)
	}
	if m.mode != modeFocus {
		t.Fatalf("turning triage on entered nothing: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("the queue opened on %q want ask", got)
	}
	for _, want := range []string{"done", "rest", "broke"} {
		updated, _ := m.handleFocusKey(ctrlQ())
		m = updated.(*Model)
		if m.mode != modeFocus {
			t.Fatalf("ctrl+q dropped out of the queue before %q: %s", want, m.errBar.text)
		}
		if got := focusedName(t, m); got != want {
			t.Fatalf("ctrl+q landed on %q want %q", got, want)
		}
	}
}

func TestNegativePrioritiesSortBelowDefault(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	for name, tier := range map[string]priority.Tier{"new-block": priority.Low, "old-block": priority.Unset} {
		if err := m.store.SetPriority(sessionID(t, m, name), tier); err != nil {
			t.Fatal(err)
		}
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))
	names := sessionNames(m)
	if slices.Index(names, "old-block") >= slices.Index(names, "new-block") {
		t.Fatalf("negative priority sorts before default: %v", names)
	}
	if rail := triageRail(m); !strings.Contains(rail, "new-block ▼") {
		t.Fatalf("negative marker missing: %s", rail)
	}
}

func TestNegativePrioritySurvivesAWaitingChild(t *testing.T) {
	for _, smart := range []bool{false, true} {
		for _, inherited := range []bool{false, true} {
			for _, childPriority := range []priority.Tier{priority.Unset, priority.Urgent} {
				t.Run(fmt.Sprintf("smart=%v/inherited=%v/child=%s", smart, inherited, childPriority.Label()), func(t *testing.T) {
					parent := smartSess("parent", status.Finished, time.Minute)
					parent.Group = "deferred"
					child := smartSess("child", status.Waiting, time.Minute)
					child.ParentID, child.Priority = parent.ID, childPriority
					m := smartFleet(t, parent, child,
						smartSess("question", status.Waiting, time.Hour),
						smartSess("done", status.Finished, time.Hour),
						smartSess("idle", status.Idle, time.Hour),
					)
					m.smartTriage = smart
					if inherited {
						m.priorityGroups = map[string]priority.Tier{"deferred": priority.Low}
					} else {
						m.sessions[0].Priority = priority.Low
					}
					m.rebuildRows()
					assertRail(t, m, "question", "done", "idle", "parent", "child")
					if got, want := drainOrder(m), []string{"question", "done", "idle", "parent"}; !slices.Equal(got, want) {
						t.Fatalf("drain = %v, want %v", got, want)
					}

					m.sessions[0].Status = status.Working
					m.rebuildRows()
					assertRail(t, m, "question", "done", "idle", "parent", "child")
					if got, want := drainOrder(m), []string{"question", "done", "idle"}; !slices.Equal(got, want) {
						t.Fatalf("drain entered a working parent: %v", got)
					}
				})
			}
		}
	}
}

func TestMediumPriorityJumpsTheTriageRing(t *testing.T) {
	m := buildModel(t)
	m.smartTriage = false // the strict order; smart triage has triagesmart_test.go
	seedTriageFleet(t, m)
	id := sessionID(t, m, "new-block")
	if err := m.store.SetPriority(id, priority.Medium); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))
	row, ok := m.nextTriageInput(sessionID(t, m, "reviewme"), nil)
	if !ok || m.rows[row].sess.ID != id {
		t.Fatal("positive priority did not jump the default sessions")
	}
}

func ctrlP() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl} }

// ctrl+p reaches the tier from the list and from inside a focused session,
// where it used to fall through to the agent as an up arrow. The palette it
// used to open is on ctrl+k.
func TestCtrlPCyclesPriorityOnBothScreens(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "rest": status.Idle})
	askID := sessionID(t, m, "ask")
	restID := sessionID(t, m, "rest")

	m.selectSessionRow(t, "rest")
	pressKey(t, m, ctrlP())
	if m.mode == modeQuickActions {
		t.Fatal("ctrl+p on the list opened quick actions")
	}
	if got, _ := m.store.Get(restID); got.Priority != priority.Urgent {
		t.Fatalf("ctrl+p on the list stored %q, want urgent", got.Priority)
	}

	m.enterFocusOn(t, "ask")
	if _, _ = m.handleFocusKey(ctrlP()); m.mode != modeFocus {
		t.Fatalf("ctrl+p left focus for mode %v", m.mode)
	}
	if got, _ := m.store.Get(askID); got.Priority != priority.Urgent {
		t.Fatalf("ctrl+p in focus stored %q, want urgent", got.Priority)
	}

	m.leaveFocus()
	pressKey(t, m, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if m.mode != modeQuickActions {
		t.Fatalf("ctrl+k left mode %v, want quick actions", m.mode)
	}
}

// A tier change spells itself out on the row and in the status bar until
// its timer runs out, and a stale timer from an earlier press cannot cut a
// newer flash short.
func TestAPriorityChangeFlashesTheRow(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "napping")
	// The press's timer is left unrun, as the runtime would for the length
	// of the flash.
	updated, _ := m.handleKey(ctrlP())
	*m = *updated.(*Model)

	flashed := "napping " + priority.Urgent.Glyph() + " URGENT"
	if rail := triageRail(m); !strings.Contains(rail, flashed) {
		t.Fatalf("the changed row does not spell its tier:\n%s", rail)
	}
	if !m.errBar.worked() || m.errBar.text != "priority urgent: napping" {
		t.Fatalf("status bar = %q, want the change named", m.errBar.text)
	}

	seq := m.priorityFlash.seq
	m.endPriorityFlash(priorityFlashEndMsg{seq: seq - 1})
	if !strings.Contains(triageRail(m), flashed) {
		t.Fatal("a stale timer ended the newer flash")
	}
	m.endPriorityFlash(priorityFlashEndMsg{seq: seq})
	if rail := triageRail(m); strings.Contains(rail, "URGENT") || !strings.Contains(rail, "napping "+priority.Urgent.Glyph()) {
		t.Fatalf("after the flash the row should wear only its glyph:\n%s", rail)
	}
}

// A background turn ended with a shell still running, which wakes it on its
// own: whatever its tier, the drain leaves it for that next turn.
func TestPriorityNeverHandsOverBackgroundTurns(t *testing.T) {
	for _, smart := range []bool{false, true} {
		for _, tier := range []priority.Tier{priority.Urgent, priority.High, priority.Medium, priority.Unset, priority.Low} {
			t.Run(fmt.Sprintf("smart=%v/tier=%s", smart, tier.Label()), func(t *testing.T) {
				monitor := smartSess("monitor", status.Background, time.Hour)
				monitor.Priority = tier
				m := smartFleet(t, monitor, smartSess("ask", status.Waiting, time.Minute))
				m.smartTriage = smart
				m.rebuildRows()
				if got := drainOrder(m); !slices.Equal(got, []string{"ask"}) {
					t.Fatalf("drain = %v, want only the question", got)
				}
				for i := range m.sessions {
					if m.sessions[i].ID == monitor.ID {
						m.sessions[i].Status = status.Working
					}
				}
				m.rebuildRows()
				if got := drainOrder(m); slices.Contains(got, "monitor") {
					t.Fatalf("drain handed over an active turn: %v", got)
				}
			})
		}
	}
}

func TestBackgroundTurnsStayOffTheDrainInSmartTriage(t *testing.T) {
	m := smartFleet(t,
		smartSess("monitor", status.Background, time.Minute),
		smartSess("idle", status.Idle, time.Hour),
		smartSess("done", status.Finished, 2*time.Hour),
	)
	if got, want := drainOrder(m), []string{"done", "idle"}; !slices.Equal(got, want) {
		t.Fatalf("drain = %v, want %v", got, want)
	}
	monitor := sessionNamed(t, m, "monitor")
	if m.needsPerson(monitor) {
		t.Fatal("a background monitor alone needs attention")
	}
	monitor.Muted = true
	if m.triageWalkable(monitor) {
		t.Fatal("a persistently muted monitor is walkable")
	}
}

func TestCreditErrorCannotJumpWaitingOrFinishedWork(t *testing.T) {
	for _, smart := range []bool{false, true} {
		t.Run(fmt.Sprintf("smart=%t", smart), func(t *testing.T) {
			m := buildModel(t)
			m.smartTriage = smart
			m.queueDeadlines = map[string]time.Time{"urgent-error": m.now().Add(time.Hour), "low-error": m.now().Add(time.Hour)}
			sessions := []store.Session{
				{ID: "urgent-error", Status: status.Errored, Priority: priority.Urgent},
				{ID: "low-error", Status: status.Errored, Priority: priority.Low},
				{ID: "finished", Status: status.Finished, Priority: priority.Low},
				{ID: "waiting", Status: status.Waiting, Priority: priority.Low},
				{ID: "muted", Status: status.Waiting, Priority: priority.Urgent, Muted: true},
			}
			m.sortTriage(sessions)
			var got []string
			for _, sess := range sessions {
				got = append(got, sess.ID)
			}
			want := []string{"waiting", "finished", "urgent-error", "low-error", "muted"}
			if !slices.Equal(got, want) {
				t.Fatalf("queue = %v, want %v", got, want)
			}
		})
	}
}
