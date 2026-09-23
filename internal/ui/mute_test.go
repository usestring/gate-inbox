package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// ctrlQWalk presses ctrl+q from a focused pane and reports where it landed, so a
// test reads as the sequence of sessions the operator was handed.
func ctrlQWalk(t *testing.T, m *Model) (*Model, string) {
	t.Helper()
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeFocus {
		return m, ""
	}
	return m, focusedName(t, m)
}

// The bug this whole file exists for: three sessions all still reading
// "waiting" because the poller has not caught up with what was typed into
// them, and an advance that cycles over the first two forever.
func TestTriageAdvanceNeverHandsBackASessionAlreadyDrained(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"first":  status.Waiting,
		"second": status.Waiting,
		"third":  status.Waiting,
	})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "first")

	var seen []string
	for range 3 {
		var name string
		if m, name = ctrlQWalk(t, m); name == "" {
			break
		}
		seen = append(seen, name)
	}
	// Nothing changed any status in between, so before mutes this walk read
	// second, first, second.
	if len(seen) != 2 || seen[0] == seen[1] {
		t.Fatalf("ctrl+q walk = %v, want the two undrained sessions once each", seen)
	}
	if m.mode != modeList {
		t.Fatalf("a drained queue left the user focused on %q", focusedName(t, m))
	}
}

// A mute is memory of one pass, not a property of the session: the moment
// the agent says something new the row is back in the queue.
func TestMuteLapsesWhenTheSessionMovesOn(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "broke": status.Errored})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")

	m, next := ctrlQWalk(t, m)
	if next != "broke" {
		t.Fatalf("first advance landed on %q, want broke", next)
	}
	asked := sessionNamed(t, m, "ask")
	if !m.isMuted(asked) {
		t.Fatal("leaving a session in triage did not mute it")
	}

	// The agent asks a fresh question: same status, later timestamp.
	asked.LastStatusAt = asked.LastStatusAt.Add(time.Minute)
	if m.isMuted(asked) {
		t.Fatal("a session that asked again is still muted")
	}
	if _, ok := m.muted[asked.ID]; ok {
		t.Fatal("a lapsed mark was not dropped")
	}
}

func TestDismissMutesAWaitingSessionAndPressingItAgainRestoresIt(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "broke": status.Errored})
	m.triage = true
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	updated, _ := m.handleKey(key("."))
	m = updated.(*Model)
	if !m.isMuted(sessionNamed(t, m, "ask")) {
		t.Fatalf(`"." did not mute a waiting session: %s`, m.errBar.text)
	}
	if rail := triageRail(m); !strings.Contains(rail, mutedGlyph()) {
		t.Fatalf("a muted row carries no mark:\n%s", rail)
	}

	updated, _ = m.handleKey(key("."))
	m = updated.(*Model)
	if m.isMuted(sessionNamed(t, m, "ask")) {
		t.Fatal(`a second "." did not un-mute the row`)
	}
}

// The finished-session reading of "." is the one it always had, and muting
// must not have quietly replaced it with a mark the store never sees.
func TestDismissStillAcksAFinishedSession(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"done": status.Finished})
	m.rebuildRows()
	m.selectSessionRow(t, "done")

	updated, _ := m.handleKey(key("."))
	m = updated.(*Model)
	sess, err := m.store.Get(sessionNamed(t, m, "done").ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess.Status != status.Idle || !sess.Acked {
		t.Fatalf(`"." left a finished session at %q acked=%v`, sess.Status, sess.Acked)
	}
	if len(m.muted) != 0 {
		t.Fatalf("acking a finished session also muted it: %v", m.muted)
	}
}

// Entering triage is a request for the whole queue, so it starts clean.
func TestEnteringTriageClearsTheLastDrainsMutes(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.selectSessionRow(t, "ask")
	updated, _ := m.handleKey(key("."))
	m = updated.(*Model)
	if len(m.muted) != 1 {
		t.Fatalf("setup did not mute: %v", m.muted)
	}

	updated, cmd := m.handleKey(key("i"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !m.triage {
		t.Fatal("i did not turn triage on")
	}
	if len(m.muted) != 0 {
		t.Fatalf("entering triage kept %v muted", m.muted)
	}
}

// Marks lapse on their own while a session lives; a session that is killed
// or archived while muted never gets the chance, so the map is swept.
func TestPruneMutesDropsMarksForSessionsThatLeftTheBoard(t *testing.T) {
	m := buildModel(t)
	m.mute(store.Session{ID: "ghost", Status: status.Waiting})
	m.mute(store.Session{ID: "here", Status: status.Waiting})
	m.pruneMutes([]store.Session{{ID: "here"}})
	if _, ok := m.muted["ghost"]; ok {
		t.Fatal("a mark for a session off the board survived")
	}
	if _, ok := m.muted["here"]; !ok {
		t.Fatal("a mark for a live session was swept")
	}
}

func handoffKey() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(triageHandoffKey)[0], Text: triageHandoffKey}
}

// § focused is ctrl+q's alias: the same mute-and-enter-the-next, so the walk
// it produces is the walk ctrl+q produces.
func TestHandoffKeyWalksTheQueueLikeCtrlQ(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"busy":  status.Working,
	})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")

	updated, _ := m.handleFocusKey(handoffKey())
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("§ left the queue instead of handing over: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "broke" {
		t.Fatalf("§ landed on %q, want broke", got)
	}
	if !m.isMuted(sessionNamed(t, m, "ask")) {
		t.Fatal("§ handed the session over without muting it")
	}

	// Nothing but the working session is left, so the drain has to let go
	// rather than offer it -- the same ending ctrl+q has.
	updated, _ = m.handleFocusKey(handoffKey())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("the queue came round again to %q", focusedName(t, m))
	}
}

// Outside triage there is no queue to walk, so the alias reads as the other
// half of ctrl+q: it leaves the session for the manager, and mutes nothing,
// because a mute only means something to a drain.
func TestHandoffKeyLeavesFocusOutsideTriage(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "broke": status.Errored})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")

	updated, _ := m.handleFocusKey(handoffKey())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("§ outside triage stayed in the session, mode %v", m.mode)
	}
	if len(m.muted) != 0 {
		t.Fatal("§ outside triage muted a session")
	}
}

// The whole point of the alias on the list: a drain can be walked from the
// moment triage is turned on, without first entering a session by hand.
func TestHandoffKeyEntersTheQueueFromTheList(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"busy":  status.Working,
	})
	m.triage = true
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	updated, cmd := m.handleKey(handoffKey())
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeFocus {
		t.Fatalf("§ on the list did not enter the next session, mode %v: %s", m.mode, m.errBar.text)
	}
	if got := focusedName(t, m); got != "broke" {
		t.Fatalf("§ on the list landed on %q, want broke", got)
	}
	if !m.isMuted(sessionNamed(t, m, "ask")) {
		t.Fatal("§ on the list did not mute the row it left")
	}
}

// On the list, outside triage, § is a key the list does not claim: it acts on
// nothing and says nothing, the way every unbound key does.
func TestHandoffKeyDoesNothingOnTheListOutsideTriage(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "broke": status.Errored})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	before := m.cursor

	updated, cmd := m.handleKey(handoffKey())
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeList || m.cursor != before {
		t.Fatalf("§ outside triage moved the cursor to %d in mode %v", m.cursor, m.mode)
	}
	if len(m.muted) != 0 || m.errBar.text != "" {
		t.Fatalf("§ outside triage muted %d rows and said %q", len(m.muted), m.errBar.text)
	}
}

// A drained queue leaves the operator on the list with nothing moving, so the
// key that did nothing has to say why.
func TestHandoffKeyOnTheListSaysWhenTheQueueIsDrained(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "busy": status.Working})
	m.triage = true
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	updated, cmd := m.handleKey(handoffKey())
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeList {
		t.Fatalf("§ entered %q with nothing else waiting", focusedName(t, m))
	}
	if !strings.Contains(m.errBar.text, "waiting on you") {
		t.Fatalf("a drained queue said %q", m.errBar.text)
	}
	if !m.isMuted(sessionNamed(t, m, "ask")) {
		t.Fatal("the row the key was pressed on was not muted")
	}
}

// The cursor is not always on the queue: a working session is on the rail in
// triage but is never handed over, and pressing on from one is a request for
// the next session that does need somebody -- without muting the row it left,
// which was never in the queue to be silenced.
func TestHandoffKeyFromARowOffTheQueue(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "busy": status.Working})
	m.triage = true
	m.rebuildRows()
	m.selectSessionRow(t, "busy")

	updated, cmd := m.handleKey(handoffKey())
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := focusedName(t, m); got != "ask" || m.mode != modeFocus {
		t.Fatalf("§ off the queue landed on %q in mode %v: %s", got, m.mode, m.errBar.text)
	}
	if len(m.muted) != 0 {
		t.Fatalf("§ muted %d rows that were never in the queue", len(m.muted))
	}
}
