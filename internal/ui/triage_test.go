package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func triageRail(m *Model) string {
	return ansi.Strip(railLinesText(m.railLines(44, m.listBodyHeight())))
}

// seedTriageFleet lays out a fleet whose triage order is nothing like its
// group order: the longest-blocked session is in one group, a second
// waiting session is folded away inside another, and the rest cover every
// remaining tier.
func seedTriageFleet(t *testing.T, m *Model) {
	t.Helper()
	base := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		sess    store.Session
		blocked time.Duration
	}{
		{store.Session{ID: "s1", Name: "old-block", Group: "alpha", Status: status.Waiting}, 0},
		{store.Session{ID: "s2", Name: "grinder", Group: "alpha", Status: status.Working}, 7 * time.Minute},
		{store.Session{ID: "s3", Name: "new-block", Group: "beta", Status: status.Waiting}, 2 * time.Minute},
		{store.Session{ID: "s4", Name: "crashed", Group: "beta", Status: status.Errored}, time.Minute},
		{store.Session{ID: "s5", Name: "reviewme", Status: status.Finished}, 3 * time.Minute},
		{store.Session{ID: "s6", Name: "napping", Status: status.Idle}, 4 * time.Minute},
		{store.Session{ID: "s7", Name: "gone", Status: status.Dead}, 5 * time.Minute},
		{store.Session{ID: "s8", Name: "booting", Status: status.Starting}, 6 * time.Minute},
	} {
		sess := seed.sess
		sess.Tool = "claude"
		sess.Cwd = "/tmp"
		sess.CreatedAt = base
		sess.LastStatusAt = base.Add(seed.blocked)
		if err := m.store.CreateSession(sess); err != nil {
			t.Fatalf("create session %q: %v", sess.ID, err)
		}
	}
	loadStoredRows(t, m)
}

func TestTriageRailOrdersByUrgencyThenOldest(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.collapsed["beta"] = true
	m.rebuildRows()

	if got := triageRail(m); strings.Contains(got, "new-block") {
		t.Fatalf("a folded group should hide new-block in normal mode:\n%s", got)
	}

	updated, cmd := m.handleKey(key("i"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !m.triage {
		t.Fatal("i did not turn triage on")
	}

	want := []string{"old-block", "new-block", "crashed", "reviewme", "napping", "grinder", "booting", "gone"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage order = %v want %v", got, want)
	}

	rail := triageRail(m)
	if !strings.Contains(rail, "TRIAGE") {
		t.Fatalf("triage mode is invisible in the rail:\n%s", rail)
	}
	at := make([]int, len(want))
	for i, name := range want {
		if at[i] = strings.Index(rail, name); at[i] < 0 {
			t.Fatalf("%q missing from the rail:\n%s", name, rail)
		}
	}
	if !slices.IsSorted(at) {
		t.Fatalf("rendered rail is not in triage order (%v):\n%s", at, rail)
	}
	if strings.Contains(rail, "alpha") || strings.Contains(rail, "beta") {
		t.Fatalf("triage should flatten the groups away:\n%s", rail)
	}
}

func TestTriageTiebreakIsOldestBlockedFirst(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.triage = true
	m.rebuildRows()
	first := sessionNames(m)[0]

	// Only the time in the state differs, so a lost tiebreak shows up as the
	// newer of the two waiting sessions being handed over first.
	if err := m.store.UpdateStatus("s1", status.Working); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)
	if got := sessionNames(m)[0]; got == first {
		t.Fatalf("head of the queue = %q even after it stopped waiting", got)
	} else if got != "new-block" {
		t.Fatalf("head of the queue = %q want new-block", got)
	}
}

func TestTriageReorderKeyRefusesAndSaysWhy(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.triage = true
	m.rebuildRows()
	before := sessionNames(m)

	m.selectSessionRow(t, "crashed")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'J', Text: "J"})
	m = updated.(*Model)
	if !strings.Contains(m.errBar.text, "press i to leave triage") {
		t.Fatalf("reorder in triage said %q", m.errBar.text)
	}
	if got := sessionNames(m); !slices.Equal(got, before) {
		t.Fatalf("reorder moved a row in triage: %v", got)
	}
}

func TestTriagePersistsAcrossToggles(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	// The toggle batches the write with the head's entry and the preview
	// it leaves scheduled; the batch has to be walked for the write to run.
	runStoreCmd(t, m.toggleTriage())
	if !storedTriage(m.store) {
		t.Fatal("triage was not persisted on")
	}
	runStoreCmd(t, m.toggleTriage())
	if storedTriage(m.store) {
		t.Fatal("triage was not persisted off")
	}
}

func TestRequiresInputIsTheAttentionFilter(t *testing.T) {
	for _, st := range []string{
		status.Waiting, status.Errored, status.Finished,
		status.Idle, status.Working, status.Starting, status.Dead,
	} {
		if requiresInput(st) != statusFilterAttention.matches(st) {
			t.Fatalf("requiresInput(%q) and the attention filter disagree", st)
		}
	}
}

// liveTriageFleet spawns real panes, since focusing one drives tmux, and
// stamps each with the status the queue should read.
func liveTriageFleet(t *testing.T, m *Model, statuses map[string]string) {
	t.Helper()
	dir := t.TempDir()
	names := make([]string, 0, len(statuses))
	for name := range statuses {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		createSession(t, m, name, dir, "")
	}
	for _, sess := range m.sessions {
		if st, ok := statuses[sess.Name]; ok {
			if err := m.store.UpdateStatus(sess.ID, st); err != nil {
				t.Fatalf("UpdateStatus %q: %v", sess.Name, err)
			}
		}
	}
	loadStoredRows(t, m)
}

func ctrlQ() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl} }

func ctrlBackslash() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl}
}

func focusedName(t *testing.T, m *Model) string {
	t.Helper()
	sess, ok := m.selected()
	if !ok {
		t.Fatal("nothing is selected")
	}
	return sess.Name
}

func (m *Model) enterFocusOn(t *testing.T, name string) {
	t.Helper()
	m.selectSessionRow(t, name)
	if _, cmd := m.focusSelected(); cmd == nil && m.mode != modeFocus {
		t.Fatalf("focusing %q failed: %s", name, m.errBar.text)
	}
	if m.mode != modeFocus {
		t.Fatalf("focusing %q left mode %v: %s", name, m.mode, m.errBar.text)
	}
}

func TestTriageAutoAdvanceWalksTheQueue(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"done":  status.Finished,
		"busy":  status.Working,
	})
	m.triage = true
	m.rebuildRows()
	if got, want := sessionNames(m), []string{"ask", "broke", "done", "busy"}; !slices.Equal(got, want) {
		t.Fatalf("queue = %v want %v", got, want)
	}

	// Starting from the middle proves the queue still wraps: the sessions
	// above the entry point are handed over after the ones below it. What it
	// does not do any more is come round a second time -- each hop mutes the
	// session it leaves, so the walk ends when the queue is drained rather
	// than cycling over work already done. See mute.go.
	m.enterFocusOn(t, "broke")
	for _, want := range []string{"done", "ask"} {
		updated, _ := m.handleFocusKey(ctrlQ())
		m = updated.(*Model)
		if m.mode != modeFocus {
			t.Fatalf("ctrl+q dropped out of the queue before %q: %s", want, m.errBar.text)
		}
		if got := focusedName(t, m); got != want {
			t.Fatalf("ctrl+q landed on %q want %q", got, want)
		}
	}
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("the queue came round again to %q", focusedName(t, m))
	}
}

func TestTriageAutoAdvanceSkipsTheSessionJustLeft(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"busy": status.Working,
	})
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode == modeFocus {
		t.Fatalf("ctrl+q bounced straight back into %q", focusedName(t, m))
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("with nothing left to answer the cursor moved to %q", got)
	}
}

func TestTriageAutoAdvanceStopsWhenTheQueueDrains(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"busy":  status.Working,
	})
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if got := focusedName(t, m); got != "broke" || m.mode != modeFocus {
		t.Fatalf("first hop landed on %q in mode %v", got, m.mode)
	}
	// broke is answered and both agents are back at work, so nothing but
	// working sessions is left and the queue has to let go rather than
	// offer one.
	if err := m.store.UpdateStatus(m.rows[m.cursor].sess.ID, status.Working); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	for i := range m.sessions {
		if m.sessions[i].Name == "broke" {
			m.sessions[i].Status = status.Working
		}
		if m.sessions[i].Name == "ask" {
			m.sessions[i].Status = status.Working
		}
	}
	m.rebuildRows()

	updated, _ = m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("a drained queue left the user in mode %v on %q", m.mode, focusedName(t, m))
	}
}

// A drain that has answered everything carries on into the idle sessions:
// nothing is waiting, but a session with nothing running is one the operator
// can give work to, and the walk is the same gesture. Each idle hop mutes
// the session it leaves, so the idle pass drains too.
func TestTriageDrainCarriesOnIntoIdleSessions(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"calm": status.Idle,
		"rest": status.Idle,
		"busy": status.Working,
	})
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	for _, want := range []string{"calm", "rest"} {
		updated, _ := m.handleFocusKey(ctrlQ())
		m = updated.(*Model)
		if m.mode != modeFocus {
			t.Fatalf("ctrl+q dropped out of the queue before %q: %s", want, m.errBar.text)
		}
		if got := focusedName(t, m); got != want {
			t.Fatalf("ctrl+q landed on %q want %q", got, want)
		}
	}
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("the idle pass came round again to %q", focusedName(t, m))
	}
}

// An idle session is never handed over while one that needs a person is
// still unanswered, whatever order the ring puts them in around the session
// just left.
func TestTriageIdleWaitsBehindEverySessionNeedingInput(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"calm":  status.Idle,
	})
	m.triage = true
	m.rebuildRows()
	if got, want := sessionNames(m), []string{"ask", "broke", "calm"}; !slices.Equal(got, want) {
		t.Fatalf("queue = %v want %v", got, want)
	}

	// From broke the ring reaches calm before it wraps to ask; ask has to
	// come first regardless.
	m.enterFocusOn(t, "broke")
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if got := focusedName(t, m); got != "ask" || m.mode != modeFocus {
		t.Fatalf("ctrl+q landed on %q in mode %v want ask", got, m.mode)
	}
	updated, _ = m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if got := focusedName(t, m); got != "calm" || m.mode != modeFocus {
		t.Fatalf("with the waiting sessions answered ctrl+q landed on %q in mode %v want calm", got, m.mode)
	}
}

// Turning triage on over a queue with nothing waiting opens the head of the
// idle pass rather than leaving the operator on the list.
func TestTriageHeadFallsBackToIdle(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"calm": status.Idle,
		"busy": status.Working,
	})
	m.applyCmd(t, m.toggleTriage())
	if m.mode != modeFocus {
		t.Fatalf("triage opened in mode %v with an idle session on its queue: %s", m.mode, m.errBar.text)
	}
	if got := focusedName(t, m); got != "calm" {
		t.Fatalf("head landed on %q want calm", got)
	}
}

func TestTriageCtrlBackslashLeavesWithoutAdvancing(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
	})
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	updated, _ := m.handleFocusKey(ctrlBackslash())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf(`ctrl+\ advanced into %q`, focusedName(t, m))
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf(`ctrl+\ moved the cursor to %q`, got)
	}
}

func TestTriageAutoAdvanceSurvivesTheFocusedSessionVanishing(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
	})
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	// The row goes while the pane is focused, the way a kill, an archive or
	// a filter takes it. The session left behind then has no place in the
	// queue to advance from, and the rest of the queue still has to be
	// handed over rather than the advance stranding on a missing anchor.
	gone := m.rows[m.cursor].sess.ID
	m.sessions = slices.DeleteFunc(m.sessions, func(sess store.Session) bool { return sess.ID == gone })
	m.rebuildRows()

	m.advanceTriage(gone)
	if m.mode != modeFocus || focusedName(t, m) != "broke" {
		t.Fatalf("advance after a vanished session ended in mode %v on %q", m.mode, focusedName(t, m))
	}
}

func TestNormalModeOrderAndCtrlQUnchanged(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	if m.triage {
		t.Fatal("triage should be off by default")
	}
	// The store's own order: root first, then each group's sessions in the
	// order they were made, untouched by status.
	want := []string{"reviewme", "napping", "gone", "booting", "old-block", "grinder", "new-block", "crashed"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("normal order = %v want %v", got, want)
	}
	if rail := triageRail(m); !strings.Contains(rail, "alpha") || strings.Contains(rail, "TRIAGE") {
		t.Fatalf("normal mode rail lost its groups or claimed triage:\n%s", rail)
	}

	live := buildModel(t)
	liveTriageFleet(t, live, map[string]string{"ask": status.Waiting, "broke": status.Errored})
	live.enterFocusOn(t, "ask")
	updated, _ := live.handleFocusKey(ctrlQ())
	live = updated.(*Model)
	if live.mode != modeList {
		t.Fatalf("ctrl+q outside triage advanced into %q", focusedName(t, live))
	}
	if got := focusedName(t, live); got != "ask" {
		t.Fatalf("ctrl+q outside triage moved the cursor to %q", got)
	}
}
