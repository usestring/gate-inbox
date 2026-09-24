package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// boardWatcher refuses a spawn named over-budget, fails to follow a
// migration when told to, and records what it heard.
type boardWatcher struct {
	failMigrate bool
	prefix      string
	shaped      []extension.Launch
	spawned     []extension.Spawn
	launches    []extension.Launch
	migrated    []extension.Migration
}

func (w *boardWatcher) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "watcher"} }
func (w *boardWatcher) Configure(extension.Config) error { return nil }
func (w *boardWatcher) ShapeSpawn(_ context.Context, launch extension.Launch) (extension.SpawnShape, error) {
	w.shaped = append(w.shaped, launch)
	return extension.SpawnShape{PromptPrefix: w.prefix}, nil
}
func (w *boardWatcher) AllowSpawn(_ context.Context, spawn extension.Spawn) error {
	if spawn.Session.Name == "over-budget" {
		return errors.New("over this goal's budget")
	}
	return nil
}
func (w *boardWatcher) Spawned(_ context.Context, spawn extension.Spawn) {
	w.spawned = append(w.spawned, spawn)
}
func (w *boardWatcher) LaunchEnv(_ context.Context, launch extension.Launch) (map[string]string, error) {
	w.launches = append(w.launches, launch)
	return nil, nil
}
func (w *boardWatcher) Migrated(_ context.Context, migration extension.Migration) error {
	w.migrated = append(w.migrated, migration)
	if w.failMigrate {
		return errors.New("cannot carry this session's state")
	}
	return nil
}

func useBoardWatcher(t *testing.T, w *boardWatcher) {
	t.Helper()
	registry, err := extension.NewRegistry([]extension.Extension{w})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Configure("", nil); err != nil {
		t.Fatal(err)
	}
	collected, err := registry.SessionHooks("", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sessionhooks.Use(func() (*extension.SessionHooks, error) { return collected, nil }))
}

// The operator's own spawn is put to the policy as the operator's, before a
// pane exists; a terminal is not put to it at all.
func TestOperatorSpawnIsPutToTheSpawnPolicy(t *testing.T) {
	m := buildModel(t)
	watcher := &boardWatcher{}
	useBoardWatcher(t, watcher)
	m.cfg.Tools["plain"] = config.Tool{Command: "cat", DefaultStatus: status.Idle}
	dir := t.TempDir()
	row := func(name string) store.Session {
		return store.Session{ID: uuid.NewString()[:8], Name: name, Tool: "plain", Cwd: dir, Status: status.Starting}
	}

	refused := row("over-budget")
	err := m.launchNewSession(refused, m.cfg.Tools["plain"], "cat")
	if err == nil || !strings.Contains(err.Error(), "over this goal's budget") {
		t.Fatalf("launchNewSession = %v, want the policy's refusal", err)
	}
	if _, err := m.store.Get(refused.ID); err == nil {
		t.Fatal("a refused spawn was stored")
	}
	if m.tmux.Exists(refused.ID) {
		t.Fatal("a refused spawn has a pane")
	}

	allowed := row("within-budget")
	if err := m.launchNewSession(allowed, m.cfg.Tools["plain"], "cat"); err != nil {
		t.Fatal(err)
	}
	if len(watcher.spawned) != 1 || watcher.spawned[0].Session.ID != allowed.ID || watcher.spawned[0].By != extension.SpawnByOperator {
		t.Fatalf("spawned = %+v", watcher.spawned)
	}
	if len(watcher.launches) != 1 || watcher.launches[0].Reason != extension.LaunchSpawn {
		t.Fatalf("launches = %+v", watcher.launches)
	}
	if len(watcher.shaped) != 0 {
		t.Fatalf("the operator's own spawn was shaped: %+v", watcher.shaped)
	}
}

// A migration from the board launches on the prompt the shapers shaped, as
// one from a session's tools does.
func TestBoardMigrationLaunchesOnTheShapedPrompt(t *testing.T) {
	m := buildModel(t)
	source, _ := seedMigrateSource(t, m)
	watcher := &boardWatcher{prefix: "GOAL: carried over"}
	useBoardWatcher(t, watcher)
	m.cfg.Tools["claude"] = config.Tool{Command: "cat", DefaultStatus: status.Idle}

	m.openMigrate()
	updated, _ := m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if m.errBar.text != "" {
		t.Fatalf("error bar = %q", m.errBar.text)
	}
	if len(watcher.shaped) != 1 || watcher.shaped[0].Reason != extension.LaunchMigrate || watcher.shaped[0].From != source.ID {
		t.Fatalf("shaped = %+v", watcher.shaped)
	}
	moved, err := m.store.Get(watcher.shaped[0].Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(moved.LaunchPrompt, "GOAL: carried over\n\nYou are taking over") {
		t.Fatalf("the migrated session launched on %q", moved.LaunchPrompt)
	}
}

// A migration from the board is followed like one from a session's tools,
// and undone when an extension cannot follow it.
func TestBoardMigrationIsUndoneWhenAnExtensionCannotFollowIt(t *testing.T) {
	m := buildModel(t)
	source, _ := seedMigrateSource(t, m)
	watcher := &boardWatcher{failMigrate: true}
	useBoardWatcher(t, watcher)
	m.cfg.Tools["claude"] = config.Tool{Command: "cat", DefaultStatus: status.Idle}

	m.openMigrate()
	updated, _ := m.handleMigrateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if !strings.Contains(m.errBar.text, "cannot carry this session's state") {
		t.Fatalf("error bar = %q, want the extension's failure", m.errBar.text)
	}
	if len(watcher.migrated) != 1 || watcher.migrated[0].From.ID != source.ID {
		t.Fatalf("migrated = %+v", watcher.migrated)
	}
	if launch := watcher.launches[0]; launch.Reason != extension.LaunchMigrate || launch.From != source.ID {
		t.Fatalf("launch = %+v", launch)
	}
	undone := watcher.migrated[0].To.ID
	if _, err := m.store.Get(undone); err == nil {
		t.Fatalf("the undone migration's row %s is still stored", undone)
	}
	if m.tmux.Exists(undone) {
		t.Fatalf("the undone migration's pane %s is still running", undone)
	}
	if len(watcher.spawned) != 0 {
		t.Fatalf("a migration was reported as a spawn: %+v", watcher.spawned)
	}
}
