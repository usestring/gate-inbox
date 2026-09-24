package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A session an extension ranks blocked sorts after the one waiting on a
// question and before the errored one, whatever its own status says, and
// goes back to its status's place once the claim is cleared.
func TestAnExtensionRanksASessionBetweenWaitingAndErrored(t *testing.T) {
	m, bridge := rowMarksModel(t)
	order := func() string {
		sessions := []store.Session{
			childSess("err", "errored", "research", "", status.Errored, 3*time.Hour),
			childSess("run", "running", "research", "", status.Working, 2*time.Hour),
			childSess("ask", "asking", "research", "", status.Waiting, time.Minute),
		}
		m.sortTriage(sessions)
		var ids []string
		for _, sess := range sessions {
			ids = append(ids, sess.ID)
		}
		return joined(ids)
	}
	if got := order(); got != "ask,err,run" {
		t.Fatalf("triage order = %s before any claim, want the working session last", got)
	}
	bridge.Attention("batch", "run", Attention{NeedsPerson: true, Rank: AttentionBlocked})
	m.Update(extensionBadgesMsg{})
	if got := order(); got != "ask,run,err" {
		t.Fatalf("triage order = %s, want the blocked session between waiting and errored", got)
	}
	// A rank alone reorders within the bucket its status puts it in; only
	// NeedsPerson moves a working session in front of the resting ones.
	bridge.Attention("batch", "run", Attention{Rank: AttentionBlocked})
	m.Update(extensionBadgesMsg{})
	if got := order(); got != "ask,err,run" {
		t.Fatalf("triage order = %s, want a ranked session that needs nobody after the ones that do", got)
	}
	bridge.Attention("batch", "run", Attention{})
	m.Update(extensionBadgesMsg{})
	if len(m.extAttention) != 0 {
		t.Fatalf("a cleared claim is still held: %v", m.extAttention)
	}
}

// A working session an extension says needs a person is on the queue: the
// attention filter keeps it, the drain may hand it over, and an ownership
// does not take it off -- until the claim is cleared.
func TestAWorkingSessionThatNeedsAPersonStaysOnTheQueue(t *testing.T) {
	m, bridge := rowMarksModel(t)
	m.statusFilter = statusFilterAttention
	m.selectSessionRow(t, "unrelated")
	listed := func() string {
		m.rebuildRows()
		var ids []string
		for _, sess := range m.listedSessions() {
			ids = append(ids, sess.ID)
		}
		return joined(ids)
	}
	if got := listed(); strings.Contains(got, "c1") {
		t.Fatalf("attention lists %s before any claim, want the working c1 left out", got)
	}
	bridge.Own("batch", "c1", true)
	bridge.Attention("other", "c1", Attention{NeedsPerson: true})
	m.Update(extensionBadgesMsg{})
	c1, _ := m.sessionByID("c1")
	if !m.needsPerson(c1) || !m.triageWalkable(c1) {
		t.Fatal("a working session an extension says needs a person is off the queue")
	}
	if got := listed(); !strings.Contains(got, "c1") || !strings.Contains(got, "p1") {
		t.Fatalf("attention lists %s, want c1 and the parent it is drawn under", got)
	}

	bridge.Attention("other", "c1", Attention{})
	m.Update(extensionBadgesMsg{})
	if m.needsPerson(c1) || m.triageWalkable(c1) {
		t.Fatal("a cleared claim left an owned working session on the queue")
	}
	if got := listed(); strings.Contains(got, "c1") {
		t.Fatalf("attention lists %s after the claim was cleared", got)
	}
}

// Where two extensions claim one session, it needs a person if either says
// so and takes the more urgent rank; one clearing its claim leaves the
// other's standing.
func TestTheMostUrgentClaimOnASessionWins(t *testing.T) {
	m, bridge := rowMarksModel(t)
	bridge.Attention("batch", "c1", Attention{NeedsPerson: true, Rank: AttentionErrored})
	bridge.Attention("other", "c1", Attention{Rank: AttentionBlocked})
	m.Update(extensionBadgesMsg{})
	if got := m.extAttention["c1"]; got != (Attention{NeedsPerson: true, Rank: AttentionBlocked}) {
		t.Fatalf("merged claim = %+v, want needs a person at the blocked rank", got)
	}
	bridge.Attention("batch", "c1", Attention{})
	m.Update(extensionBadgesMsg{})
	if got := m.extAttention["c1"]; got != (Attention{Rank: AttentionBlocked}) {
		t.Fatalf("claim after one cleared = %+v, want the other's standing", got)
	}
}

// A parent is lifted to the tier of a child an extension says needs a
// person, and the child is drawn under it -- even when the child sits on a
// dialog its live parent could answer, which would otherwise fold it into
// the parent and leave the parent queued at its own working tier.
func TestAParentIsLiftedToItsFlaggedChildsTier(t *testing.T) {
	for _, tc := range []struct {
		name       string
		child      string
		answerable bool
	}{
		{"a working child", status.Working, false},
		{"a child on a dialog its parent could answer", status.Waiting, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, bridge := rowMarksModel(t)
			m.sessions = []store.Session{
				childSess("p1", "worker", "research", "", status.Working, time.Hour),
				childSess("c1", "probe", "research", "p1", tc.child, time.Minute),
				childSess("e1", "broken", "research", "", status.Errored, 3*time.Hour),
			}
			m.answerableWait = map[string]bool{"c1": tc.answerable}
			m.triage = true
			m.rebuildRows()
			before := joined(rowIDs(m))

			bridge.Attention("runs", "c1", Attention{NeedsPerson: true, Rank: AttentionBlocked})
			m.Update(extensionBadgesMsg{})
			m.rebuildRows()
			if got := joined(rowIDs(m)); got != "p1,c1,e1" {
				t.Fatalf("triage rows = %s (before the claim %s), want the parent lifted ahead of the errored session with its child under it", got, before)
			}
		})
	}
}
