package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The mute key stores a flag on the session and the row wears it as its
// state: "muted" in place of the pane status, kept on the list rather than
// dropped from it.
func TestMuteKeyStoresTheFlagAndTheRowReadsMuted(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "busy": status.Working})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	updated, _ := m.handleKey(key("M"))
	m = updated.(*Model)
	ask := sessionNamed(t, m, "ask")
	if !ask.Muted {
		t.Fatalf("M did not mute: %s", m.errBar.text)
	}
	stored, err := m.store.Get(ask.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !stored.Muted {
		t.Fatal("the mute never reached the store")
	}
	rail := triageRail(m)
	if !strings.Contains(rail, mutedStatusLabel) {
		t.Fatalf("a muted row does not read %q:\n%s", mutedStatusLabel, rail)
	}
	if strings.Contains(rail, "waiting") {
		t.Fatalf("a muted row still reads its pane status:\n%s", rail)
	}
	if m.needsPerson(ask) || m.triageWalkable(ask) {
		t.Fatal("a muted session still counts as on the operator's queue")
	}

	// The same key puts it back.
	updated, _ = m.handleKey(key("M"))
	m = updated.(*Model)
	if sessionNamed(t, m, "ask").Muted {
		t.Fatal("M did not unmute")
	}
	stored, err = m.store.Get(sessionNamed(t, m, "ask").ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Muted {
		t.Fatal("unmuting never reached the store")
	}
}

// A persistently muted session never reappears in triage, on the rail or in the
// drain, until it is explicitly unmuted.
func TestMutedSessionStaysOutOfTriageUntilUnmuted(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "other": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	updated, _ := m.handleKey(key("M"))
	m = updated.(*Model)

	m.triage = true
	m.rebuildRows()
	if rail := triageRail(m); strings.Contains(rail, "ask") {
		t.Fatalf("a muted session reappeared in triage:\n%s", rail)
	}
	if names := sessionNames(m); len(names) != 1 || names[0] != "other" {
		t.Fatalf("triage queue = %v, want only other", names)
	}
	if cmd := m.enterTriageHead(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := focusedName(t, m); got != "other" {
		t.Fatalf("the drain handed over %q, want other", got)
	}

	// Unmuting from the list brings the row back to the queue.
	m.mode = modeList
	m.triage = false
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	updated, _ = m.handleKey(key("M"))
	m = updated.(*Model)
	m.triage = true
	m.rebuildRows()
	if rail := triageRail(m); !strings.Contains(rail, "ask") {
		t.Fatalf("an unmuted session did not return to triage:\n%s", rail)
	}
}

// The skip key on a muted row is the way back: it un-mutes rather than
// refusing the row as one that is not waiting on anybody.
func TestSkipKeyUnmutesADurablyMutedRow(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	updated, _ := m.handleKey(key("M"))
	m = updated.(*Model)

	updated, _ = m.handleKey(key("."))
	m = updated.(*Model)
	if sessionNamed(t, m, "ask").Muted {
		t.Fatalf(`"." did not unmute the row: %s`, m.errBar.text)
	}
}

// A muted child is off triage the same as a muted top-level row. It is not
// drawn under its parent, and it does not lift that parent up the queue.
func TestMutedChildStaysOutOfTriage(t *testing.T) {
	m := childModel(t)
	for i := range m.sessions {
		if m.sessions[i].ID == "c2" {
			m.sessions[i].Muted = true
		}
	}
	m.triage = true
	m.rebuildRows()
	if got := joined(rowIDs(m)); strings.Contains(got, "c2") {
		t.Fatalf("triage rows = %q, want the muted child left out", got)
	}
}

func TestMutedChildDoesNotLiftItsParent(t *testing.T) {
	parent := childSess("p1", "worker", "build", "", status.Working, time.Hour)
	waiting := childSess("c1", "helper", "build", "p1", status.Waiting, 30*time.Minute)
	waiting.Muted = true
	idle := childSess("s9", "other", "build", "", status.Idle, 2*time.Hour)
	queue := []store.Session{parent, idle}
	kids := map[string][]store.Session{"p1": {waiting}}
	(&Model{}).sortTriageWithChildren(queue, kids)
	if queue[0].ID != "s9" {
		t.Fatalf("queue leads with %s, want the idle row, since a muted child lifts nothing", queue[0].ID)
	}
}

// A muted row reads "muted", so search finds it by that word.
func TestSearchFindsAMutedRowByItsLabel(t *testing.T) {
	sess := childSess("c1", "helper", "build", "", status.Waiting, time.Minute)
	if matchesMetadata(sess, mutedStatusLabel) {
		t.Fatal("an unmuted row matched the muted label")
	}
	sess.Muted = true
	if !matchesLiteralMetadata(sess, mutedStatusLabel) {
		t.Fatal("literal search missed a muted row by the label it shows")
	}
	if _, ok := fuzzyMetadataScore(sess, mutedStatusLabel); !ok {
		t.Fatal("fuzzy search missed a muted row by the label it shows")
	}
}

// Muting the selected row under w takes it off the filter: the hold that keeps
// the cursor's row listed is for a status that moved, not for a mute.
func TestAttentionFilterDropsTheRowMutedUnderTheCursor(t *testing.T) {
	m := childModel(t)
	m.statusFilter = statusFilterAttention
	m.rebuildRows()
	for i, row := range m.rows {
		if row.isSession() && row.sess.ID == "c2" {
			m.cursor = i
		}
	}
	if sess, ok := m.selected(); !ok || sess.ID != "c2" {
		t.Fatal("the waiting child is not selectable under w")
	}
	for i := range m.sessions {
		if m.sessions[i].ID == "c2" {
			m.sessions[i].Muted = true
		}
	}
	for _, sess := range m.computeListedSessions() {
		if sess.ID == "c2" {
			t.Fatal("the row muted under the cursor stayed on w")
		}
	}
}

// The attention filter keeps a muted branch out: a muted row's own escalation
// does not hold it on w, and a muted child does not hold its parent there.
func TestAttentionFilterLeavesMutedBranchesOut(t *testing.T) {
	m := childModel(t)
	m.statusFilter = statusFilterAttention
	parent := m.sessions[0]
	if !m.attentionViaChild(parent) {
		t.Fatal("the children needing a person did not hold their parent on w before any mute")
	}
	for i := range m.sessions {
		if m.sessions[i].ParentID == parent.ID {
			m.sessions[i].Muted = true
		}
	}
	if m.attentionViaChild(parent) {
		t.Fatal("muted children still hold their parent on w")
	}
	m.extAttention = map[string]Attention{parent.ID: {NeedsPerson: true}}
	parent.Muted = true
	if m.attentionViaChild(parent) {
		t.Fatal("a muted row's own escalation still holds it on w")
	}
}

// A muted parent takes its unmuted children off w with it. Without that the
// waiting child stays listed, loses its parent, and paints as a top-level row.
func TestAttentionFilterDropsChildrenOfAMutedParent(t *testing.T) {
	m := childModel(t)
	m.statusFilter = statusFilterAttention
	m.sessions[0].Muted = true
	m.rebuildRows()
	for _, sess := range m.computeListedSessions() {
		if sess.ID == "p1" || sess.ParentID == "p1" {
			t.Fatalf("%s from the muted branch stayed on w", sess.ID)
		}
	}
	if got := joined(rowIDs(m)); strings.Contains(got, "c2") {
		t.Fatalf("rows = %q, want the muted parent's waiting child left out", got)
	}
}

// A muted row that is still working shows the mute mark on its conversation
// status row, not the working spinner.
func TestMutedWorkingRowDropsTheSpinner(t *testing.T) {
	m := childModel(t)
	m.sessions[0].Muted = true
	m.rebuildRows()
	m.conversation = &conversationView{hovered: -1}
	for i, row := range m.rows {
		if row.isSession() && row.sess.ID == "p1" {
			m.cursor = i
		}
	}
	if !m.conversationWorking() {
		t.Fatal("the working row is not drawn for the selected working session")
	}
	rows := m.withWorkingRow(nil, 80)
	got := ansi.Strip(rows[len(rows)-1])
	if !strings.HasPrefix(got, mutedGlyph()) || !strings.Contains(got, mutedStatusLabel) {
		t.Fatalf("working row = %q, want the mute mark beside %q", got, mutedStatusLabel)
	}
	for _, frame := range startupFrames {
		if strings.HasPrefix(got, ansi.Strip(frame)) {
			t.Fatalf("working row = %q still leads with the spinner", got)
		}
	}
}
