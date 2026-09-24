package extension_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// shaper shapes every launch it is asked about the same way, and records
// what it was asked.
type shaper struct {
	id     string
	shape  extension.SpawnShape
	refuse error
	panics bool
	asked  []extension.Launch
}

func (s *shaper) Descriptor() extension.Descriptor { return extension.Descriptor{ID: s.id} }
func (s *shaper) Configure(extension.Config) error { return nil }
func (s *shaper) ShapeSpawn(_ context.Context, launch extension.Launch) (extension.SpawnShape, error) {
	s.asked = append(s.asked, launch)
	if s.panics {
		panic("broken shaper")
	}
	return s.shape, s.refuse
}

func TestSessionHooksJoinEveryShapersPrefixInOrder(t *testing.T) {
	goal := &shaper{id: "goal", shape: extension.SpawnShape{PromptPrefix: "  Work to the goal.\n"}}
	blank := &shaper{id: "blank"}
	keep := &shaper{id: "keep", shape: extension.SpawnShape{PromptPrefix: "Report to your spawner.", KeepUnderSpawner: true}}
	hooks := sessionHooks(t, goal, blank, &stub{id: "tools-only"}, keep)

	launch := extension.Launch{Session: extension.SessionInfo{ID: "abcd1234"}, Reason: extension.LaunchMigrate, From: "0ld5e551"}
	shape, err := hooks.ShapeSpawn(context.Background(), launch)
	if err != nil {
		t.Fatal(err)
	}
	want := "Work to the goal.\n\nReport to your spawner."
	if shape.PromptPrefix != want {
		t.Fatalf("PromptPrefix = %q, want %q", shape.PromptPrefix, want)
	}
	if !shape.KeepUnderSpawner {
		t.Fatal("one shaper keeping the spawn under its spawner did not keep it")
	}
	for _, s := range []*shaper{goal, blank, keep} {
		if len(s.asked) != 1 || s.asked[0] != launch {
			t.Fatalf("%s was asked %+v, want %+v", s.id, s.asked, launch)
		}
	}
	if got := shape.Prefixed("do the thing"); got != want+"\n\ndo the thing" {
		t.Fatalf("Prefixed = %q", got)
	}
}

func TestSessionHooksTreatAFailingShaperAsARefusal(t *testing.T) {
	later := &shaper{id: "later"}
	hooks := sessionHooks(t, &shaper{id: "budget", refuse: errors.New("no goal to brief")}, later)
	_, err := hooks.ShapeSpawn(context.Background(), extension.Launch{})
	if err == nil || !strings.Contains(err.Error(), `extension "budget" refused the launch: no goal to brief`) {
		t.Fatalf("ShapeSpawn = %v", err)
	}
	if len(later.asked) != 0 {
		t.Fatal("a shaper was asked after an earlier one refused")
	}

	hooks = sessionHooks(t, &shaper{id: "broken", panics: true})
	if _, err := hooks.ShapeSpawn(context.Background(), extension.Launch{}); err == nil || !strings.Contains(err.Error(), "panicked: broken shaper") {
		t.Fatalf("ShapeSpawn = %v, want the panic as a refusal", err)
	}
}

func TestAnEmptyShapeLeavesThePromptAlone(t *testing.T) {
	var hooks *extension.SessionHooks
	shape, err := hooks.ShapeSpawn(context.Background(), extension.Launch{})
	if err != nil || shape != (extension.SpawnShape{}) {
		t.Fatalf("nil hooks shaped %+v, %v", shape, err)
	}
	if got := shape.Prefixed("as asked"); got != "as asked" {
		t.Fatalf("Prefixed = %q", got)
	}
	if got := (extension.SpawnShape{PromptPrefix: "brief"}).Prefixed(""); got != "brief" {
		t.Fatalf("Prefixed with no prompt = %q", got)
	}
}
