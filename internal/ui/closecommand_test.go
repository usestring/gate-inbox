package ui

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/closecmd"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// recordCloses swaps the close command for a recorder. Not parallel: the
// hook is package state.
func recordCloses(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	previous := sessionClosed
	sessionClosed = func(_ config.Config, reason string, sessions ...store.Session) {
		mu.Lock()
		defer mu.Unlock()
		for _, sess := range sessions {
			calls = append(calls, sess.ID+":"+reason)
		}
	}
	t.Cleanup(func() { sessionClosed = previous })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

// The board's own closes run the close command: a finished child it retires
// through x's teardown, and an exited child it files away, which may have
// died without its session-end hooks.
func TestTheChildSweepRunsTheCloseCommandForWhatItFiles(t *testing.T) {
	calls := recordCloses(t)

	m, parent, kid := finishedFanOut(t)
	relayRest(t, m, parent, kid, true)
	runChildSweep(t, m)
	if row, err := m.store.Get(kid.ID); err != nil || !row.Archived {
		t.Fatalf("the sweep did not retire the finished child: %+v, %v", row, err)
	}
	if got, want := calls(), []string{kid.ID + ":" + closecmd.ReasonArchived}; !slices.Equal(got, want) {
		t.Fatalf("after retiring, closes = %v, want %v", got, want)
	}

	m, parent, kid = finishedFanOut(t)
	before := len(calls())
	report, _, err := m.store.Enqueue(store.InboxMessage{
		SessionID: parent.ID, SenderID: kid.ID, SenderName: kid.Name,
		Body: "report: done", SentAt: time.Now(),
	}, store.DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := m.store.MarkDelivered(report, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if err := m.tmux.Kill(kid.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := m.store.UpdateStatus(kid.ID, status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)
	runChildSweep(t, m)
	if row, err := m.store.Get(kid.ID); err != nil || !row.Archived {
		t.Fatalf("the sweep did not file the exited child: %+v, %v", row, err)
	}
	if got, want := calls()[before:], []string{kid.ID + ":" + closecmd.ReasonArchived}; !slices.Equal(got, want) {
		t.Fatalf("after filing, closes = %v, want %v", got, want)
	}
	if slices.ContainsFunc(calls(), func(c string) bool { return strings.HasPrefix(c, parent.ID+":") }) {
		t.Fatalf("the parent was closed: %v", calls())
	}
}
