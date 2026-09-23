package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

// An agent spawning an agent is a fan-out, and the list draws it under the
// session that asked for it. create_terminal has always recorded the parent;
// create_session did not, so every child an agent spawned landed as a
// sibling of its parent and the tree never nested.
func TestSessionsCreateRecordsTheCallerAsParent(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{
		Name:   "probe-one",
		Prompt: "read the payload builder",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored, err := h.store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.ParentID != h.caller.ID {
		t.Fatalf("parent = %q, want the calling session %q", stored.ParentID, h.caller.ID)
	}
}

// A group asked for while nesting would be silently dropped by the store,
// which forces a child into its parent's group -- and the cwd and worktree
// already resolved from that group would go with it. It is refused instead,
// in create_terminal's words.
func TestSessionsCreateRefusesAnotherGroupWhileNesting(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	elsewhere := "somewhere-else"
	if err := h.store.CreateGroup(elsewhere, ""); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	_, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "probe-two", Group: &elsewhere})
	if err == nil || !strings.Contains(err.Error(), "set nest false") {
		t.Fatalf("Create err = %v, want the nest refusal", err)
	}
}

// A group that does not exist is reported as missing, not as a nesting
// conflict: the refusal must not shadow the more useful error.
func TestSessionsCreateStillReportsAMissingGroup(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	missing := "no-such-group"
	_, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "probe", Group: &missing})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Create err = %v, want the missing-group error", err)
	}
}

// nest false is how a spawn goes somewhere else: no parent, and the group
// asked for is the group it lands in.
func TestSessionsCreateUnnestedTakesTheRequestedGroup(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	elsewhere := "somewhere-else"
	if err := h.store.CreateGroup(elsewhere, ""); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	unnested := false
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{
		Name: "probe-three", Group: &elsewhere, Nest: &unnested,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored, err := h.store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.ParentID != "" {
		t.Fatalf("un-nested session has parent %q", stored.ParentID)
	}
	if stored.Group != elsewhere {
		t.Fatalf("group = %q, want %q", stored.Group, elsewhere)
	}
}

// The store carries one level of parenthood, so a grandchild is filed beside
// the caller rather than under it. Erroring instead would make a child that
// fans out again fail for a reason nothing in its prompt explains.
func TestSessionsCreateFlattensAGrandchildBesideItsCaller(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	child, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "probe-one"})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	grand, err := h.sessions.Create(child.ID, CreateSessionOptions{Name: "probe-one-a"})
	if err != nil {
		t.Fatalf("Create grandchild: %v", err)
	}
	stored, err := h.store.Get(grand.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.ParentID != h.caller.ID {
		t.Fatalf("grandchild parent = %q, want it flattened onto %q", stored.ParentID, h.caller.ID)
	}
}

// The row is drawn beside the caller and owned by it. Placement and ownership
// were one column and had to give the same answer; they no longer do, and the
// grandchild is where this first shows.
func TestSessionsCreateRecordsTheSpawnerOfAGrandchild(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	child, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "probe-one"})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	grand, err := h.sessions.Create(child.ID, CreateSessionOptions{Name: "probe-one-a"})
	if err != nil {
		t.Fatalf("Create grandchild: %v", err)
	}
	stored, err := h.store.Get(grand.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.ParentID != h.caller.ID {
		t.Fatalf("grandchild parent = %q, want it flattened onto %q", stored.ParentID, h.caller.ID)
	}
	if stored.SpawnedBy != child.ID {
		t.Fatalf("grandchild spawned_by = %q, want the session that spawned it %q", stored.SpawnedBy, child.ID)
	}
	if grand.SpawnedBy != child.ID {
		t.Fatalf("returned spawned_by = %q, want %q", grand.SpawnedBy, child.ID)
	}
}

// A child that fans out has children, and used to be told it had none: every
// one of its spawns was filed beside it, so the fan-out read off the tree was
// empty. Nine spawners on one board hit this.
func TestSendChildrenReachesAGrandchildFromItsSpawner(t *testing.T) {
	h := newSessionHarness(t)
	child := childRow(t, h, store.Session{
		ID: "a1b2c3d4", Name: "spawner", ParentID: h.caller.ID, SpawnedBy: h.caller.ID,
	}, true)
	grand := childRow(t, h, store.Session{
		ID: "a1b2c3d5", Name: "grandchild", ParentID: h.caller.ID, SpawnedBy: child.ID,
	}, true)

	sent, err := h.sessions.SendChildren(child.ID, "the branch moved")
	if err != nil {
		t.Fatalf("SendChildren from the spawner: %v", err)
	}
	if sent.Queued != 1 || len(sent.Deliveries) != 1 {
		t.Fatalf("queued %d of %d, want the one grandchild", sent.Queued, len(sent.Deliveries))
	}
	if sent.Deliveries[0].SessionID != grand.ID {
		t.Fatalf("delivered to %q, want %q", sent.Deliveries[0].SessionID, grand.ID)
	}
	// And the root, which did not spawn it, reaches only what it did spawn.
	fromRoot, err := h.sessions.SendChildren(h.caller.ID, "carry on")
	if err != nil {
		t.Fatalf("SendChildren from the root: %v", err)
	}
	if len(fromRoot.Deliveries) != 1 || fromRoot.Deliveries[0].SessionID != child.ID {
		t.Fatalf("root reached %+v, want only %q", fromRoot.Deliveries, child.ID)
	}
}

// answer_session refused a spawner its own child's dialog and offered it to a
// root that had not set the task -- df8785d4 was told at 19:31:33 on
// 2026-09-17 that a session it had spawned itself belonged to somebody else.
func TestChildResolvesBySpawnerNotByPlacement(t *testing.T) {
	h := newSessionHarness(t)
	child := childRow(t, h, store.Session{
		ID: "b1c2d3e4", Name: "spawner", ParentID: h.caller.ID, SpawnedBy: h.caller.ID,
	}, true)
	grand := childRow(t, h, store.Session{
		ID: "b1c2d3e5", Name: "grandchild", ParentID: h.caller.ID, SpawnedBy: child.ID,
	}, true)
	runtime, err := h.sessions.open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer runtime.store.Close()
	spawner, err := runtime.caller(child.ID)
	if err != nil {
		t.Fatalf("caller: %v", err)
	}
	if _, err := runtime.child(spawner, grand.ID); err != nil {
		t.Fatalf("spawner refused its own grandchild: %v", err)
	}
	root, err := runtime.caller(h.caller.ID)
	if err != nil {
		t.Fatalf("caller: %v", err)
	}
	_, err = runtime.child(root, grand.ID)
	if err == nil || !strings.Contains(err.Error(), child.ID) {
		t.Fatalf("root err = %v, want a refusal naming the spawner %q", err, child.ID)
	}
}
