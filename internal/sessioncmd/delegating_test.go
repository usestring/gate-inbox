package sessioncmd

import (
	"context"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// delegatingTree is the caller's child at rest on a grandchild of its own
// that is still at work. Grandchildren are filed under the root and owned
// through spawned_by.
func delegatingTree(t *testing.T, h *sessionHarness) (mid, leaf store.Session) {
	t.Helper()
	mid = childRow(t, h, store.Session{ID: "mids-aa1", Name: "mid", ParentID: h.caller.ID, SpawnedBy: h.caller.ID, Status: status.Finished}, true)
	leaf = childRow(t, h, store.Session{ID: "leaf-bb2", Name: "leaf", ParentID: h.caller.ID, SpawnedBy: mid.ID, Status: status.Working}, true)
	return mid, leaf
}

func TestListReportsAParentAtRestOnItsFanOutAsDelegating(t *testing.T) {
	h := newSessionHarness(t)
	mid, _ := delegatingTree(t, h)

	list, err := h.sessions.List(h.caller.ID, ListOptions{Status: []string{status.Delegating}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// The caller is idle on its own pane with mid still going under it, so
	// it delegates too; the working leaf does not.
	ids := map[string]bool{}
	for _, sess := range list.Sessions {
		ids[sess.ID] = true
	}
	if len(ids) != 2 || !ids[mid.ID] || !ids[h.caller.ID] {
		t.Fatalf("delegating rows = %+v, want %s and the caller", list.Sessions, mid.ID)
	}
	got, err := h.sessions.Get(h.caller.ID, mid.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != status.Delegating {
		t.Fatalf("get_session status = %q, want delegating", got.Status)
	}
	// The row itself is untouched: the pane's reading is what is stored.
	if row, _ := h.store.Get(mid.ID); row.Status != status.Finished {
		t.Fatalf("stored status = %q, want finished", row.Status)
	}
}

func TestWaitDoesNotTakeADelegatingChildAsStopped(t *testing.T) {
	h := newSessionHarness(t)
	mid, leaf := delegatingTree(t, h)

	result, err := h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{mid.ID}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Reached || result.Session.Status != status.Delegating {
		t.Fatalf("wait on a delegating child = %+v, want a timeout carrying delegating", result)
	}

	result, err = h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{mid.ID}, Until: []string{status.Delegating}, Timeout: time.Second})
	if err != nil {
		t.Fatalf("Wait until delegating: %v", err)
	}
	if !result.Reached {
		t.Fatalf("wait until delegating = %+v, want reached", result)
	}

	if err := h.store.UpdateStatus(leaf.ID, status.Finished); err != nil {
		t.Fatal(err)
	}
	result, err = h.sessions.Wait(context.Background(), h.caller.ID, WaitOptions{SessionIDs: []string{mid.ID}, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !result.Reached || result.Session.Status != status.Finished {
		t.Fatalf("wait once the grandchild is done = %+v, want finished", result)
	}
}

func TestBoardGetReportsDelegatingUntilDescendantsFinish(t *testing.T) {
	for _, resting := range []string{status.Finished, status.Idle} {
		t.Run(resting, func(t *testing.T) {
			h := newSessionHarness(t)
			mid, leaf := delegatingTree(t, h)
			if err := h.store.UpdateStatus(mid.ID, resting); err != nil {
				t.Fatal(err)
			}
			got, err := h.sessions.BoardGet(mid.ID)
			if err != nil {
				t.Fatalf("BoardGet: %v", err)
			}
			if got.Status != status.Delegating || got.Self {
				t.Fatalf("BoardGet = %+v, want delegating without self", got)
			}
			if row, err := h.store.Get(mid.ID); err != nil || row.Status != resting {
				t.Fatalf("stored row = %+v, %v; want status %q", row, err, resting)
			}
			if err := h.store.UpdateStatus(leaf.ID, status.Finished); err != nil {
				t.Fatal(err)
			}
			got, err = h.sessions.BoardGet(mid.ID)
			if err != nil || got.Status != resting {
				t.Fatalf("BoardGet after descendants finish = %+v, %v; want status %q", got, err, resting)
			}
		})
	}
}
