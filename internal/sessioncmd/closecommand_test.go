package sessioncmd

import (
	"slices"
	"sync"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/closecmd"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
)

type closeCall struct{ id, reason string }

// recordCloses swaps the close command for a recorder. Not parallel: the
// hook is package state, and a parallel test only resumes once every
// sequential one has finished.
func recordCloses(t *testing.T) func() []closeCall {
	t.Helper()
	var mu sync.Mutex
	var calls []closeCall
	previous := sessionClosed
	sessionClosed = func(_ config.Config, reason string, sessions ...store.Session) {
		mu.Lock()
		defer mu.Unlock()
		for _, sess := range sessions {
			calls = append(calls, closeCall{sess.ID, reason})
		}
	}
	t.Cleanup(func() { sessionClosed = previous })
	return func() []closeCall {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

// A kill and an archive are closes, so each runs the close command once for
// the session it closed; an archive's fan-out runs it for every row filed.
func TestKillAndArchiveRunTheCloseCommand(t *testing.T) {
	calls := recordCloses(t)
	h := newSessionHarness(t)
	killed := restingRow(t, h, "kill0001")
	parent := restingRow(t, h, "arch0001")
	child := childRow(t, h, store.Session{ID: "kid00001", Name: "kid", ParentID: parent.ID, SpawnedBy: parent.ID}, true)

	if _, err := h.sessions.Kill(h.caller.ID, killed.ID, extension.KillByMCP); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got, want := calls(), []closeCall{{killed.ID, closecmd.ReasonKilled}}; !slices.Equal(got, want) {
		t.Fatalf("after kill, closes = %+v, want %+v", got, want)
	}
	if _, err := h.sessions.Archive(h.caller.ID, parent.ID, true); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got := calls()[1:]
	for _, id := range []string{parent.ID, child.ID} {
		if !slices.Contains(got, closeCall{id, closecmd.ReasonArchived}) {
			t.Errorf("archive closes = %+v, want %s archived", got, id)
		}
	}
	if len(got) != 2 {
		t.Errorf("archive closes = %+v, want one per filed row", got)
	}
}

// A hibernated session resumes on its conversation, so hibernating is not a
// close, and neither is restoring an archived row.
func TestHibernateAndRestoreDoNotRunTheCloseCommand(t *testing.T) {
	calls := recordCloses(t)
	h := newSessionHarness(t)
	rest := restingRow(t, h, "rest0001")
	if _, err := h.sessions.Hibernate(h.caller.ID, rest.ID, false); err != nil {
		t.Fatalf("Hibernate: %v", err)
	}
	if h.driver.Exists(rest.ID) {
		t.Fatal("hibernate left the agent running")
	}
	if got := calls(); len(got) != 0 {
		t.Fatalf("hibernate ran the close command: %+v", got)
	}
	filed := restingRow(t, h, "file0001")
	if _, err := h.sessions.Archive(h.caller.ID, filed.ID, true); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	before := len(calls())
	if _, err := h.sessions.Archive(h.caller.ID, filed.ID, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := calls(); len(got) != before {
		t.Fatalf("restoring ran the close command: %+v", got[before:])
	}
}
