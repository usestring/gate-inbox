package sessionhooks

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/store"
)

// childCap refuses a spawn under a parent that already has max live
// children, counted through the reader the spawn carries.
type childCap struct {
	max     int
	spawned []extension.Spawn
}

func (c *childCap) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "cap"} }
func (c *childCap) Configure(extension.Config) error { return nil }

func (c *childCap) AllowSpawn(ctx context.Context, spawn extension.Spawn) error {
	list, err := spawn.Sessions.List(ctx, extension.SessionFilter{ParentID: spawn.Session.ParentID})
	if err != nil {
		return err
	}
	live := 0
	for _, child := range list.Sessions {
		if child.Running {
			live++
		}
	}
	if live >= c.max {
		return fmt.Errorf("%s already has %d live children", spawn.Session.ParentID, live)
	}
	return nil
}

func (c *childCap) Spawned(_ context.Context, spawn extension.Spawn) {
	c.spawned = append(c.spawned, spawn)
}

// boardOf is a board of fixed rows.
type boardOf []extension.SessionInfo

func (b boardOf) Get(_ context.Context, id string) (extension.SessionInfo, error) {
	for _, sess := range b {
		if sess.ID == id {
			return sess, nil
		}
	}
	return extension.SessionInfo{}, errors.New("no such session")
}

func (b boardOf) List(_ context.Context, filter extension.SessionFilter) (extension.SessionList, error) {
	var list extension.SessionList
	for _, sess := range b {
		if filter.ParentID == "" || sess.ParentID == filter.ParentID {
			list.Sessions = append(list.Sessions, sess)
		}
	}
	list.Matched = len(list.Sessions)
	return list, nil
}

func useCap(t *testing.T, c *childCap) {
	t.Helper()
	registry, err := extension.NewRegistry([]extension.Extension{c})
	if err != nil {
		t.Fatal(err)
	}
	collected, err := registry.SessionHooks("", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Use(func() (*extension.SessionHooks, error) { return collected, nil }))
}

func TestASpawnPolicyReadsTheBoardBeforeTheSpawn(t *testing.T) {
	policy := &childCap{max: 2}
	useCap(t, policy)
	board := boardOf{
		{ID: "a0000001", ParentID: "p0000001", Running: true},
		{ID: "a0000002", ParentID: "p0000001", Running: false},
		{ID: "a0000003", ParentID: "p0000002", Running: true},
	}
	t.Cleanup(UseSessions(board))

	hooks, err := CheckSpawn(store.Session{ID: "b0000001", ParentID: "p0000001"}, extension.SpawnBySession)
	if err != nil {
		t.Fatalf("one live child of two allowed: %v", err)
	}
	Spawned(hooks, store.Session{ID: "b0000001", ParentID: "p0000001"}, extension.SpawnBySession)
	if len(policy.spawned) != 1 || policy.spawned[0].Sessions == nil {
		t.Fatalf("Spawned was told %+v, want the reader too", policy.spawned)
	}

	board[1].Running = true
	_, err = CheckSpawn(store.Session{ID: "b0000002", ParentID: "p0000001"}, extension.SpawnBySession)
	if err == nil || err.Error() != `extension "cap" refused the spawn: p0000001 already has 2 live children` {
		t.Fatalf("CheckSpawn = %v, want the cap's refusal", err)
	}
}

// A process nobody gave a reader refuses a policy that reads, rather than
// letting it count an empty board.
func TestASpawnPolicyWithNoBoardToReadRefuses(t *testing.T) {
	useCap(t, &childCap{max: 1})
	t.Cleanup(UseSessions(unread{}))
	_, err := CheckSpawn(store.Session{ID: "b0000001", ParentID: "p0000001"}, extension.SpawnBySession)
	if !errors.Is(err, errUnread) {
		t.Fatalf("CheckSpawn = %v, want %v", err, errUnread)
	}
}
