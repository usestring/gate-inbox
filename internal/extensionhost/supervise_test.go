package extensionhost

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// rolesBoard is a Board whose sessions are only an id and a role.
type rolesBoard struct {
	extension.Board
	roles map[string]string
}

func (b rolesBoard) Get(_ context.Context, id string) (extension.SessionInfo, error) {
	role, ok := b.roles[id]
	if !ok {
		return extension.SessionInfo{}, fmt.Errorf("no session %s", id)
	}
	return extension.SessionInfo{ID: id, Role: role}, nil
}

func viewOf(events *Events, board extension.Board, owner string) *boardView {
	return &boardView{Board: board, events: events, owner: owner}
}

func TestSupervisedPinsBelongToTheSupervisingExtension(t *testing.T) {
	ctx := context.Background()
	events := NewEvents(NewBoard("", nil), nil)
	refreshed := 0
	events.OnPinChange(func() { refreshed++ })
	board := rolesBoard{roles: map[string]string{
		"worker":   "",
		"reviewer": "ext1/reviewer",
		"linter":   "other/linter",
	}}
	ext1, other := viewOf(events, board, "ext1"), viewOf(events, board, "other")

	if err := ext1.Supervise(ctx, "worker", true); err != nil {
		t.Fatal(err)
	}
	if err := ext1.PinStatus(ctx, "worker", " waiting "); err != nil {
		t.Fatal(err)
	}
	if got, ok := events.PinnedStatus("worker"); !ok || got != "waiting" {
		t.Fatalf("pinned = %q, %v", got, ok)
	}
	if refreshed != 1 {
		t.Fatalf("a pin asked for %d passes, want 1", refreshed)
	}

	// Another extension can neither take the worker over, let it go, nor
	// pin it; nor can it supervise a session launched for ext1's role.
	if err := other.Supervise(ctx, "worker", true); err == nil || !strings.Contains(err.Error(), `supervised by extension "ext1"`) {
		t.Fatalf("a second claim = %v", err)
	}
	if err := other.Supervise(ctx, "worker", false); err == nil {
		t.Fatal("another extension let the worker go")
	}
	if err := other.Supervise(ctx, "reviewer", true); err == nil || !strings.Contains(err.Error(), "for a role of its own") {
		t.Fatalf("supervising another's role session = %v", err)
	}
	// A session of its own role it may supervise.
	if err := ext1.Supervise(ctx, "reviewer", true); err != nil {
		t.Fatal(err)
	}
	if err := ext1.PinStatus(ctx, "worker", "dead"); err == nil || !strings.Contains(err.Error(), "not a status to pin") {
		t.Fatalf("pinning dead = %v", err)
	}
	if got, _ := events.PinnedStatus("worker"); got != "waiting" {
		t.Fatalf("a refused pin changed it to %q", got)
	}

	if err := ext1.PinStatus(ctx, "worker", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := events.PinnedStatus("worker"); ok {
		t.Fatal("releasing the pin kept it")
	}
	if err := ext1.PinStatus(ctx, "worker", "finished"); err != nil {
		t.Fatal(err)
	}
	if err := ext1.Supervise(ctx, "worker", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := events.PinnedStatus("worker"); ok {
		t.Fatal("letting the worker go kept its pin")
	}
	// Now nobody's, it is the other extension's to take.
	if err := other.Supervise(ctx, "worker", true); err != nil {
		t.Fatal(err)
	}
	if err := other.Supervise(ctx, "missing", true); err == nil {
		t.Fatal("supervised a session that does not exist")
	}
}

// A stopped extension holds nothing still, and cannot claim again from a
// goroutine it left behind.
func TestReleaseLetsGoOfEverythingSupervised(t *testing.T) {
	ctx := context.Background()
	events := NewEvents(NewBoard("", nil), nil)
	refreshed := 0
	events.OnPinChange(func() { refreshed++ })
	board := rolesBoard{roles: map[string]string{"a": "", "b": ""}}
	ext1, other := viewOf(events, board, "ext1"), viewOf(events, board, "other")
	for _, id := range []string{"a", "b"} {
		view := ext1
		if id == "b" {
			view = other
		}
		if err := view.Supervise(ctx, id, true); err != nil {
			t.Fatal(err)
		}
		if err := view.PinStatus(ctx, id, "waiting"); err != nil {
			t.Fatal(err)
		}
	}
	refreshed = 0
	ext1.release()
	if _, ok := events.PinnedStatus("a"); ok {
		t.Fatal("a released extension's pin held")
	}
	if got, _ := events.PinnedStatus("b"); got != "waiting" {
		t.Fatalf("another extension's pin = %q", got)
	}
	if refreshed != 1 {
		t.Fatalf("release asked for %d passes, want 1", refreshed)
	}
	if err := ext1.Supervise(ctx, "a", true); err == nil {
		t.Fatal("a released extension claimed again")
	}
}
