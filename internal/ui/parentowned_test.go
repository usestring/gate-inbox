package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// ownedBoard is one live parent with one child stopped on a question, which
// is the shape the triage rule is about.
func ownedBoard(t *testing.T, parent, child store.Session) *Model {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m := &Model{store: st, poller: &poller{store: st}}
	m.sessions = []store.Session{parent, child}
	// The shape the rule is about is a child on a dialog its parent can
	// answer; the pass marks that, and a board built without it is the
	// unanswerable case each test names for itself.
	m.answerableWait = map[string]bool{child.ID: true}
	return m
}

func liveParent() store.Session {
	return store.Session{ID: "parent01", Name: "site-graph-endpoint", Tool: "claude", Status: status.Working}
}

func stoppedChild(waitingFor time.Duration) store.Session {
	return store.Session{
		ID: "child001", Name: "sampleapp-reach-census", Tool: "claude",
		Status: status.Waiting, ParentID: "parent01",
		LastStatusAt: time.Now().Add(-waitingFor),
	}
}

func TestParentOwnsAChildItsLiveParentCanAnswer(t *testing.T) {
	m := ownedBoard(t, liveParent(), stoppedChild(time.Minute))
	if !m.parentOwns(m.sessions[1], time.Now(), map[string]bool{"parent01": true}) {
		t.Error("a live parent's child reads as unowned, so triage would queue it for a person")
	}
}

func TestParentOwnsNothingWhenNobodyIsComing(t *testing.T) {
	cases := []struct {
		name   string
		parent store.Session
		child  store.Session
	}{
		{
			name:   "a dead parent",
			parent: store.Session{ID: "parent01", Tool: "claude", Status: status.Dead},
			child:  stoppedChild(time.Minute),
		},
		{
			name:   "an archived parent",
			parent: store.Session{ID: "parent01", Tool: "claude", Status: status.Idle, Archived: true},
			child:  stoppedChild(time.Minute),
		},
		{
			// A parent standing on its own question is in the queue, not
			// working through it.
			name:   "a parent waiting on a person itself",
			parent: store.Session{ID: "parent01", Tool: "claude", Status: status.Waiting},
			child:  stoppedChild(time.Minute),
		},
		{
			name:   "a question left standing past the grace period",
			parent: liveParent(),
			child:  stoppedChild(ownedGrace + time.Minute),
		},
		{
			name:   "a top-level session, which has no parent to own it",
			parent: liveParent(),
			child:  store.Session{ID: "child001", Tool: "claude", Status: status.Waiting, LastStatusAt: time.Now()},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := ownedBoard(t, tc.parent, tc.child)
			if m.parentOwns(tc.child, time.Now(), map[string]bool{"parent01": true}) {
				t.Error("read as owned, so triage would hide a question nobody is going to answer")
			}
		})
	}
}

// The queue's lift exists so a child blocked on a person is not buried under
// its working parent. A child blocked on its parent is not blocked on a
// person, so it must not drag the parent up the queue either.
// The row keeps the status of its last poll, so a parent whose CLI has
// exited reads as idle until something notices. An idle row with no process
// behind it answers nothing, and its children are the operator's.
func TestParentOwnsNothingOnceItsPaneIsGone(t *testing.T) {
	m := ownedBoard(t, liveParent(), stoppedChild(time.Minute))
	if m.parentOwns(m.sessions[1], time.Now(), map[string]bool{}) {
		t.Error("a parent with no live pane reads as owning its child's question")
	}
}

func TestTriageDoesNotLiftAParentForAQuestionItOwns(t *testing.T) {
	parent := liveParent()
	owned := stoppedChild(time.Minute)
	m := ownedBoard(t, parent, owned)
	other := store.Session{ID: "other001", Name: "unowned", Tool: "claude",
		Status: status.Waiting, LastStatusAt: time.Now().Add(-2 * time.Minute)}
	m.sessions = append(m.sessions, other)

	sessions := []store.Session{parent, other}
	m.sortTriageWithChildren(sessions, map[string][]store.Session{parent.ID: {owned}})
	if sessions[0].ID != other.ID {
		t.Errorf("queue leads with %s, want the session actually blocked on a person", sessions[0].ID)
	}
}

func TestTriageStillLiftsAParentForAQuestionNobodyOwns(t *testing.T) {
	parent := liveParent()
	orphaned := stoppedChild(ownedGrace + time.Minute)
	m := ownedBoard(t, parent, orphaned)
	working := store.Session{ID: "other001", Name: "busy", Tool: "claude",
		Status: status.Working, LastStatusAt: time.Now()}
	m.sessions = append(m.sessions, working)

	sessions := []store.Session{working, parent}
	m.sortTriageWithChildren(sessions, map[string][]store.Session{parent.ID: {orphaned}})
	if sessions[0].ID != parent.ID {
		t.Errorf("queue leads with %s, want the parent lifted for its stranded child", sessions[0].ID)
	}
}

// A permission prompt is not the parent's to answer -- answer_session has no
// keys for it -- so folding it away hides it from the one person who can.
func TestParentOwnsNothingItCannotAnswer(t *testing.T) {
	m := ownedBoard(t, liveParent(), stoppedChild(time.Minute))
	m.answerableWait = map[string]bool{}
	if m.parentOwns(m.sessions[1], time.Now(), map[string]bool{"parent01": true}) {
		t.Error("a permission-blocked child reads as its parent's, so triage would hide it from the operator")
	}
}
