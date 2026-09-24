package sessioncmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
)

// launchWatcher is an extension with a say in every launch: it refuses what
// refuse names, adds env to every launch, and records everything it is told.
type launchWatcher struct {
	refuse      string
	failMigrate bool
	env         map[string]string
	shape       extension.SpawnShape

	mu       sync.Mutex
	shaped   []extension.Launch
	asked    []extension.Spawn
	spawned  []extension.Spawn
	launches []extension.Launch
	migrated []extension.Migration
}

func (w *launchWatcher) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "watcher"} }
func (w *launchWatcher) Configure(extension.Config) error { return nil }

func (w *launchWatcher) ShapeSpawn(_ context.Context, launch extension.Launch) (extension.SpawnShape, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shaped = append(w.shaped, launch)
	return w.shape, nil
}

func (w *launchWatcher) AllowSpawn(_ context.Context, spawn extension.Spawn) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked = append(w.asked, spawn)
	if w.refuse != "" && spawn.Session.Name == w.refuse {
		return errors.New("over this goal's budget")
	}
	return nil
}

func (w *launchWatcher) Spawned(_ context.Context, spawn extension.Spawn) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.spawned = append(w.spawned, spawn)
}

func (w *launchWatcher) LaunchEnv(_ context.Context, launch extension.Launch) (map[string]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.launches = append(w.launches, launch)
	return w.env, nil
}

func (w *launchWatcher) Migrated(_ context.Context, migration extension.Migration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.migrated = append(w.migrated, migration)
	if w.failMigrate {
		return errors.New("cannot carry this session's state")
	}
	return nil
}

// useWatcher makes w the process's only extension for the rest of the test.
// The hooks are the process's, so a test calling this is never parallel.
func useWatcher(t *testing.T, w *launchWatcher) {
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

// envEchoTool prints the one variable the watcher contributes, then waits.
const envEchoTool = `
[tools.envecho]
command = "sh -c 'echo contributed=$P6_WATCHER_MARK; sleep 30' --"
default_status = "idle"
`

func addEnvEchoTool(t *testing.T, h *sessionHarness) {
	t.Helper()
	path := filepath.Join(h.sessions.configDir, "config.toml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(envEchoTool); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAsksTheSpawnPolicyBeforeLaunching(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{refuse: "too-many"}
	useWatcher(t, watcher)

	before, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "too-many", Prompt: "go"})
	if err == nil || !strings.Contains(err.Error(), "over this goal's budget") {
		t.Fatalf("Create = %v, want the policy's refusal", err)
	}
	after, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("a refused spawn left %d rows, want %d", len(after), len(before))
	}
	if len(watcher.spawned) != 0 || len(watcher.launches) != 0 {
		t.Fatalf("a refused spawn was launched: spawned %v, launches %v", watcher.spawned, watcher.launches)
	}
	asked := watcher.asked[0]
	if asked.By != extension.SpawnBySession || asked.Session.SpawnedBy != h.caller.ID || asked.Session.ParentID != h.caller.ID {
		t.Fatalf("policy was asked %+v", asked)
	}
	if h.driver.Exists(asked.Session.ID) {
		t.Fatalf("a refused spawn has a pane")
	}

	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "allowed", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(watcher.spawned) != 1 || watcher.spawned[0].Session.ID != created.ID || watcher.spawned[0].By != extension.SpawnBySession {
		t.Fatalf("spawned = %+v, want the one that launched", watcher.spawned)
	}
}

func TestLaunchesCarryTheContributedEnvironment(t *testing.T) {
	h := newSessionHarness(t)
	addEnvEchoTool(t, h)
	watcher := &launchWatcher{env: map[string]string{"P6_WATCHER_MARK": "from-the-extension"}}
	useWatcher(t, watcher)

	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "envecho", Name: "env"})
	if err != nil {
		t.Fatal(err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, created.ID, "contributed=from-the-extension")
	launch := watcher.launches[0]
	if launch.Reason != extension.LaunchSpawn || launch.Session.ID != created.ID || launch.Session.Tool != "envecho" {
		t.Fatalf("launch = %+v", launch)
	}

	watcher.env = map[string]string{hooks.EnvSessionID: "someone-else"}
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "envecho"}); err == nil || !strings.Contains(err.Error(), "the launch sets itself") {
		t.Fatalf("Create with the session id overridden = %v, want it refused", err)
	}
}

func TestMigrateTellsObserversAndUndoesWhatTheyCannotFollow(t *testing.T) {
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)
	watcher := &launchWatcher{}
	useWatcher(t, watcher)

	moved, err := h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "echoer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(watcher.migrated) != 1 || watcher.migrated[0].From.ID != source.ID || watcher.migrated[0].To.ID != moved.ID {
		t.Fatalf("migrated = %+v", watcher.migrated)
	}
	if launch := watcher.launches[0]; launch.Reason != extension.LaunchMigrate || launch.From != source.ID || launch.Session.ID != moved.ID {
		t.Fatalf("launch = %+v", launch)
	}
	if len(watcher.asked) != 0 {
		t.Fatalf("a migration was put to the spawn policy: %+v", watcher.asked)
	}

	watcher.failMigrate = true
	_, err = h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "echoer", Name: "second"})
	if err == nil || !strings.Contains(err.Error(), "cannot carry") {
		t.Fatalf("Migrate = %v, want the observer's failure", err)
	}
	undone := watcher.migrated[1].To.ID
	if _, err := h.store.Get(undone); err == nil {
		t.Fatalf("the undone migration's row %s is still stored", undone)
	}
	if h.driver.Exists(undone) {
		t.Fatalf("the undone migration's pane %s is still running", undone)
	}
}

func TestBoardLaunchFilesARoleHelperUnderItsParent(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{}
	useWatcher(t, watcher)

	created, err := h.sessions.BoardLaunch(BoardLaunchOptions{
		Tool:     "echoer",
		Name:     "helper",
		Prompt:   "relay this",
		ParentID: h.caller.ID,
		Role:     "watcher/helper",
		Args:     []string{"--allowed", "one tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Role != "watcher/helper" || created.ParentID != h.caller.ID || created.Group != h.caller.Group {
		t.Fatalf("created = %+v", created)
	}
	stored, err := h.store.Get(created.ID)
	if err != nil || stored.Role != "watcher/helper" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if len(watcher.asked) != 1 || watcher.asked[0].By != extension.SpawnByExtension || watcher.asked[0].Session.Role != "watcher/helper" {
		t.Fatalf("asked = %+v", watcher.asked)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, created.ID, "relay this --allowed one tool")
}

func TestCreateLaunchesTheShapedSpawn(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{shape: extension.SpawnShape{PromptPrefix: "GOAL: ship it", KeepUnderSpawner: true}}
	useWatcher(t, watcher)
	detach := false

	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "kept", Prompt: "the sub-task", Nest: &detach})
	if err != nil {
		t.Fatal(err)
	}
	shaped := watcher.shaped[0]
	if shaped.Reason != extension.LaunchSpawn || shaped.Session.ID != created.ID || shaped.Session.SpawnedBy != h.caller.ID || shaped.Session.ParentID != "" {
		t.Fatalf("the shaper was asked %+v, want the detached spawn as asked for", shaped)
	}
	if asked := watcher.asked[0]; asked.Session.ParentID != h.caller.ID {
		t.Fatalf("the policy was asked about parent %q, want the shaped %q", asked.Session.ParentID, h.caller.ID)
	}
	if created.ParentID != h.caller.ID || created.Group != h.caller.Group {
		t.Fatalf("created = %+v, want it kept under %s", created, h.caller.ID)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, created.ID, "GOAL: ship it")
	stored, err := h.store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ParentID != h.caller.ID || !strings.Contains(stored.LaunchPrompt, "GOAL: ship it\n\nthe sub-task") {
		t.Fatalf("stored parent %q, prompt %q", stored.ParentID, stored.LaunchPrompt)
	}

	watcher.shape = extension.SpawnShape{}
	loose, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "loose", Prompt: "elsewhere", Nest: &detach})
	if err != nil {
		t.Fatal(err)
	}
	if loose.ParentID != "" {
		t.Fatalf("an unshaped detached spawn was filed under %q", loose.ParentID)
	}
}

func TestMigrateLaunchesOnTheShapedPrompt(t *testing.T) {
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)
	watcher := &launchWatcher{shape: extension.SpawnShape{PromptPrefix: "GOAL: carried over"}}
	useWatcher(t, watcher)

	moved, err := h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "echoer"})
	if err != nil {
		t.Fatal(err)
	}
	shaped := watcher.shaped[0]
	if shaped.Reason != extension.LaunchMigrate || shaped.From != source.ID || shaped.Session.ID != moved.ID {
		t.Fatalf("the shaper was asked %+v", shaped)
	}
	// The brief is longer than the pane, so the stored prompt is read
	// rather than the screen.
	stored, err := h.store.Get(moved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.LaunchPrompt, "GOAL: carried over\n\nYou are taking over") {
		t.Fatalf("the migrated session launched on %q", stored.LaunchPrompt)
	}
}
