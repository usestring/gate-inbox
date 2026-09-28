package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// row files a session with no pane of its own: placement is a store question,
// and none of these cases needs a screen.
func row(t *testing.T, h *sessionHarness, sess store.Session) store.Session {
	t.Helper()
	if sess.Tool == "" {
		sess.Tool = "echoer"
	}
	if sess.Group == "" {
		sess.Group = h.caller.Group
	}
	if sess.Status == "" {
		sess.Status = status.Idle
	}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatalf("create %s: %v", sess.ID, err)
	}
	return sess
}

// The case this exists for: a spawn that landed as the caller's sibling
// because the manager build serving it predated nesting.
func TestAdoptClaimsAParentlessSession(t *testing.T) {
	h := newSessionHarness(t)
	orphan := row(t, h, store.Session{ID: "orphan01", Name: "sampleapp-brain-jobs"})
	placed, err := h.sessions.AdoptSession(h.caller.ID, orphan.ID)
	if err != nil {
		t.Fatalf("AdoptSession: %v", err)
	}
	if placed.ParentID != h.caller.ID {
		t.Errorf("parent = %q, want the caller %q", placed.ParentID, h.caller.ID)
	}
	stored, err := h.store.Get(orphan.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.ParentID != h.caller.ID {
		t.Errorf("stored parent = %q, want the caller", stored.ParentID)
	}
}

func TestAdoptIsIdempotentOnItsOwnChild(t *testing.T) {
	h := newSessionHarness(t)
	child := row(t, h, store.Session{ID: "child001", Name: "mine", ParentID: h.caller.ID})
	placed, err := h.sessions.AdoptSession(h.caller.ID, child.ID)
	if err != nil {
		t.Fatalf("AdoptSession: %v", err)
	}
	if placed.ParentID != h.caller.ID {
		t.Errorf("parent = %q, want it left where it was", placed.ParentID)
	}
}

// Rearranging somebody else's fan-out is the board's job. The refusal names
// the owner because the caller's next move is to ask it.
func TestAdoptRefusesAnotherSessionsChild(t *testing.T) {
	h := newSessionHarness(t)
	other := row(t, h, store.Session{ID: "other001", Name: "another-agent"})
	theirs := row(t, h, store.Session{ID: "theirs01", Name: "their-child", ParentID: other.ID})
	_, err := h.sessions.AdoptSession(h.caller.ID, theirs.ID)
	if err == nil || !strings.Contains(err.Error(), other.ID) {
		t.Fatalf("AdoptSession err = %v, want a refusal naming %s", err, other.ID)
	}
}

func TestReleaseTakesTheCallersOwnChildToTheTopLevel(t *testing.T) {
	h := newSessionHarness(t)
	child := row(t, h, store.Session{ID: "child002", Name: "own-work", ParentID: h.caller.ID})
	placed, err := h.sessions.ReleaseSession(h.caller.ID, child.ID)
	if err != nil {
		t.Fatalf("ReleaseSession: %v", err)
	}
	if placed.ParentID != "" {
		t.Errorf("parent = %q, want it released", placed.ParentID)
	}
	// The group is where a person filed the work; letting go of a child is
	// not a decision about that.
	if placed.Group != h.caller.Group {
		t.Errorf("group = %q, want it kept at %q", placed.Group, h.caller.Group)
	}
}

// A live parent's children stay its own: the parent is still there to let
// them go, so nobody else may.
func TestReleaseRefusesASessionItDoesNotOwn(t *testing.T) {
	h := newSessionHarness(t)
	other := row(t, h, store.Session{ID: "other002", Name: "another-agent"})
	if err := h.driver.Create(other.ID, t.TempDir(), "", nil, 80, 24); err != nil {
		t.Fatalf("create parent pane: %v", err)
	}
	theirs := row(t, h, store.Session{ID: "theirs02", Name: "their-child", ParentID: other.ID})
	_, err := h.sessions.ReleaseSession(h.caller.ID, theirs.ID)
	if err == nil {
		t.Fatal("released a session the caller does not own")
	}
	if !strings.Contains(err.Error(), "only a parent releases its own children") {
		t.Errorf("ReleaseSession err = %v, want the parent-only refusal", err)
	}
	stored, err := h.store.Get(theirs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.ParentID != other.ID {
		t.Errorf("stored parent = %q, want it left under %q", stored.ParentID, other.ID)
	}
}

// A parent that is gone will never release its children, so any session may.
// Each case files the child under a parent that cannot act: one with no pane,
// one archived (with a pane, so the archive alone is what counts), and one
// whose row was deleted out from under its children.
func TestReleaseFreesTheChildOfAGoneParent(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, h *sessionHarness, parent store.Session)
	}{
		{name: "dead", setup: func(*testing.T, *sessionHarness, store.Session) {}},
		{name: "archived", setup: func(t *testing.T, h *sessionHarness, parent store.Session) {
			if err := h.driver.Create(parent.ID, t.TempDir(), "", nil, 80, 24); err != nil {
				t.Fatalf("create parent pane: %v", err)
			}
			if err := h.store.SetArchived(parent.ID, true); err != nil {
				t.Fatalf("SetArchived: %v", err)
			}
		}},
		{name: "missing", setup: func(t *testing.T, h *sessionHarness, parent store.Session) {
			if err := h.store.Delete(parent.ID); err != nil {
				t.Fatalf("Delete: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newSessionHarness(t)
			parent := row(t, h, store.Session{ID: "parent03", Name: "gone-parent"})
			child := row(t, h, store.Session{ID: "child003", Name: "stranded", ParentID: parent.ID})
			tc.setup(t, h, parent)
			placed, err := h.sessions.ReleaseSession(h.caller.ID, child.ID)
			if err != nil {
				t.Fatalf("ReleaseSession: %v", err)
			}
			if placed.ParentID != "" {
				t.Errorf("parent = %q, want it released", placed.ParentID)
			}
			if placed.Group != child.Group {
				t.Errorf("group = %q, want it kept at %q", placed.Group, child.Group)
			}
		})
	}
}

func TestPlaceRefusesTheCallersOwnRow(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	if _, err := h.sessions.AdoptSession(h.caller.ID, h.caller.ID); err == nil {
		t.Fatal("a session adopted itself")
	}
}
