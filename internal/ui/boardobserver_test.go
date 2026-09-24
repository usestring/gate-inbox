package ui

import (
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

type recordedMove struct{ id, from, to string }

type recordingObserver struct {
	mu     sync.Mutex
	moves  []recordedMove
	passes [][]store.Session
}

func (r *recordingObserver) Transition(id, from, to string, _ time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.moves = append(r.moves, recordedMove{id, from, to})
}

func (r *recordingObserver) Pass(_ time.Time, sessions []store.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.passes = append(r.passes, sessions)
}

// A pass reports each status change it stores, once, and every pass it
// finishes, including the ones that moved nothing.
func TestPollPassReportsStoredTransitionsToTheObserver(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	m.form.name.SetValue("observed")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = toolIndexOf(t, m, "claude-hooked")
	pickGroup(t, m, "")
	_, cmd := m.submitForm()
	m.applyCmd(t, cmd)
	m.leaveFocusForFixture(t)
	sess := m.sessionRows()[0]
	if err := m.store.UpdateStatus(sess.ID, status.Working); err != nil {
		t.Fatal(err)
	}
	observer := &recordingObserver{}
	m.ObserveBoard(observer)

	for _, hook := range []string{status.Finished, status.Finished, status.Waiting} {
		writeHookStatus(t, m, sess.ID, hook)
		if msg, failed := m.poller.refreshOnce().(errMsg); failed {
			t.Fatalf("pass: %v", msg.err)
		}
	}

	want := []recordedMove{
		{sess.ID, status.Working, status.Finished},
		{sess.ID, status.Finished, status.Waiting},
	}
	if len(observer.moves) != len(want) {
		t.Fatalf("moves = %v, want %v", observer.moves, want)
	}
	for i := range want {
		if observer.moves[i] != want[i] {
			t.Fatalf("moves = %v, want %v", observer.moves, want)
		}
	}
	if len(observer.passes) != 3 {
		t.Fatalf("passes reported = %d, want 3", len(observer.passes))
	}
	last := observer.passes[2]
	if len(last) != 1 || last[0].ID != sess.ID || last[0].Status != status.Waiting {
		t.Fatalf("last pass = %+v, want the session waiting", last)
	}
}
