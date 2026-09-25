package extension_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// launcher has a say in sessions: it refuses when told to, adds env, and
// logs what it is asked into a log shared with the others in a test.
type launcher struct {
	id       string
	off      bool
	refuse   error
	panicAt  string
	env      map[string]string
	failMove error
	log      *[]string
	settings struct {
		Budget int `toml:"budget"`
	}
}

func (l *launcher) Descriptor() extension.Descriptor { return extension.Descriptor{ID: l.id} }
func (l *launcher) Configure(cfg extension.Config) error {
	*l.log = append(*l.log, "configure "+l.id)
	return cfg.Decode(&l.settings)
}
func (l *launcher) Enabled() bool { return !l.off }

func (l *launcher) at(step string) {
	*l.log = append(*l.log, step+" "+l.id)
	if l.panicAt == step {
		panic("broken " + step)
	}
}

func (l *launcher) AllowSpawn(context.Context, extension.Spawn) error {
	l.at("allow")
	return l.refuse
}
func (l *launcher) Spawned(context.Context, extension.Spawn) { l.at("spawned") }
func (l *launcher) LaunchEnv(context.Context, extension.Launch) (map[string]string, error) {
	l.at("env")
	return l.env, nil
}
func (l *launcher) Migrated(context.Context, extension.Migration) error {
	l.at("migrated")
	return l.failMove
}

func (l *launcher) Killed(_ context.Context, kill extension.KillContext) {
	l.at("killed " + kill.Session.ID + " " + string(kill.Via))
}

func sessionHooks(t *testing.T, exts ...extension.Extension) *extension.SessionHooks {
	t.Helper()
	hooks, err := mustRegistry(t, exts...).SessionHooks("", nil)
	if err != nil {
		t.Fatal(err)
	}
	return hooks
}

func TestSessionHooksAskEnabledPoliciesInOrderUntilOneRefuses(t *testing.T) {
	var log []string
	hooks := sessionHooks(t,
		&launcher{id: "first", log: &log},
		&launcher{id: "off", off: true, log: &log},
		&stub{id: "tools-only"},
		&launcher{id: "budget", refuse: errors.New("over budget"), log: &log},
		&launcher{id: "last", log: &log},
	)
	log = nil
	err := hooks.AllowSpawn(context.Background(), extension.Spawn{})
	if err == nil || !strings.Contains(err.Error(), `extension "budget" refused the spawn: over budget`) {
		t.Fatalf("AllowSpawn = %v", err)
	}
	if want := []string{"allow first", "allow budget"}; !slices.Equal(log, want) {
		t.Fatalf("asked %q, want %q", log, want)
	}
}

func TestSessionHooksTreatAPanickingPolicyAsARefusal(t *testing.T) {
	var log []string
	hooks := sessionHooks(t, &launcher{id: "broken", panicAt: "allow", log: &log})
	err := hooks.AllowSpawn(context.Background(), extension.Spawn{})
	if err == nil || !strings.Contains(err.Error(), "panicked: broken allow") {
		t.Fatalf("AllowSpawn = %v, want the panic as a refusal", err)
	}
}

func TestSessionHooksTellEveryPolicyOfASpawnEvenAfterOnePanics(t *testing.T) {
	var log []string
	hooks := sessionHooks(t,
		&launcher{id: "broken", panicAt: "spawned", log: &log},
		&launcher{id: "counter", log: &log},
	)
	log = nil
	err := hooks.Spawned(context.Background(), extension.Spawn{})
	if err == nil || !strings.Contains(err.Error(), `extension "broken"`) {
		t.Fatalf("Spawned = %v, want the panic reported", err)
	}
	if want := []string{"spawned broken", "spawned counter"}; !slices.Equal(log, want) {
		t.Fatalf("told %q, want %q", log, want)
	}
}

func TestSessionHooksMergeLaunchEnvAndRefuseAClash(t *testing.T) {
	var log []string
	hooks := sessionHooks(t,
		&launcher{id: "ext1", env: map[string]string{"STATE_FILE": "/ext1/a.json", "INHERITED": ""}, log: &log},
		&launcher{id: "traces", env: map[string]string{"TRACE_TAG": "t-7"}, log: &log},
	)
	env, err := hooks.LaunchEnv(context.Background(), extension.Launch{Reason: extension.LaunchSpawn})
	if err != nil {
		t.Fatal(err)
	}
	if env["STATE_FILE"] != "/ext1/a.json" || env["TRACE_TAG"] != "t-7" || len(env) != 3 {
		t.Fatalf("env = %v", env)
	}
	if value, set := env["INHERITED"]; !set || value != "" {
		t.Fatalf("an empty value must survive the merge, to withhold an inherited one: %v", env)
	}

	clash := sessionHooks(t,
		&launcher{id: "one", env: map[string]string{"SHARED": "a"}, log: &log},
		&launcher{id: "two", env: map[string]string{"SHARED": "b"}, log: &log},
	)
	if _, err := clash.LaunchEnv(context.Background(), extension.Launch{}); err == nil || !strings.Contains(err.Error(), `extensions "one" and "two" both set SHARED`) {
		t.Fatalf("LaunchEnv = %v, want the clash refused", err)
	}
	bad := sessionHooks(t, &launcher{id: "bad", env: map[string]string{"NOT-A-NAME": "x"}, log: &log})
	if _, err := bad.LaunchEnv(context.Background(), extension.Launch{}); err == nil || !strings.Contains(err.Error(), "not an environment variable name") {
		t.Fatalf("LaunchEnv = %v, want the name refused", err)
	}
}

func TestSessionHooksStopTellingOfAMigrationAtTheFirstFailure(t *testing.T) {
	var log []string
	hooks := sessionHooks(t,
		&launcher{id: "carrier", failMove: errors.New("state is locked"), log: &log},
		&launcher{id: "after", log: &log},
	)
	log = nil
	err := hooks.Migrated(context.Background(), extension.Migration{})
	if err == nil || !strings.Contains(err.Error(), `extension "carrier" could not follow the migration: state is locked`) {
		t.Fatalf("Migrated = %v", err)
	}
	if want := []string{"migrated carrier"}; !slices.Equal(log, want) {
		t.Fatalf("told %q, want %q", log, want)
	}
}

func TestNilSessionHooksAllowEverything(t *testing.T) {
	var hooks *extension.SessionHooks
	ctx := context.Background()
	if err := hooks.AllowSpawn(ctx, extension.Spawn{}); err != nil {
		t.Fatal(err)
	}
	if env, err := hooks.LaunchEnv(ctx, extension.Launch{}); err != nil || env != nil {
		t.Fatalf("LaunchEnv = %v, %v", env, err)
	}
	if err := hooks.Migrated(ctx, extension.Migration{}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionHooksConfigureOnlyTheirOwnOnAnUnconfiguredRegistry(t *testing.T) {
	var log []string
	registry, err := extension.NewRegistry([]extension.Extension{
		&launcher{id: "budget", log: &log},
		&stub{id: "tools-only"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// tools-only's section would fail its Decode; a launch never reads it.
	sections := map[string]map[string]any{
		"budget":     {"budget": 3},
		"tools-only": {"unknown": true},
	}
	hooks, err := registry.SessionHooks(t.TempDir(), sections)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"configure budget"}; !slices.Equal(log, want) {
		t.Fatalf("configured %q, want %q", log, want)
	}
	if registry.Configured() {
		t.Fatal("configuring the hooks alone must not mark the registry configured")
	}
	if err := hooks.AllowSpawn(context.Background(), extension.Spawn{}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionHooksTellEveryEnabledObserverOfAKillInOrder(t *testing.T) {
	var log []string
	hooks := sessionHooks(t,
		&launcher{id: "broken", panicAt: "killed a1 cli", log: &log},
		&launcher{id: "off", off: true, log: &log},
		&stub{id: "tools-only"},
		&launcher{id: "extra", log: &log},
	)
	log = nil
	err := hooks.Killed(context.Background(), extension.KillContext{
		Session: extension.SessionInfo{ID: "a1"}, Via: extension.KillByCLI,
	})
	if err == nil || !strings.Contains(err.Error(), `extension "broken": panicked: broken killed a1 cli`) {
		t.Fatalf("Killed = %v, want the panic reported", err)
	}
	if want := []string{"killed a1 cli broken", "killed a1 cli extra"}; !slices.Equal(log, want) {
		t.Fatalf("told %q, want %q", log, want)
	}
	var none *extension.SessionHooks
	if err := none.Killed(context.Background(), extension.KillContext{}); err != nil {
		t.Fatalf("nil hooks: %v", err)
	}
}

type sendHandler struct {
	id     string
	result string
	err    error
	panics bool
}

func (s *sendHandler) Descriptor() extension.Descriptor { return extension.Descriptor{ID: s.id} }
func (s *sendHandler) Configure(extension.Config) error { return nil }
func (s *sendHandler) OperatorSent(context.Context, extension.OperatorSend) (string, error) {
	if s.panics {
		panic("broken handler")
	}
	return s.result, s.err
}

func TestSessionHooksCollectOperatorSendResultsPastAFailure(t *testing.T) {
	hooks := sessionHooks(t,
		&sendHandler{id: "ext1", panics: true},
		&sendHandler{id: "ext2", result: "answered"},
		&sendHandler{id: "ext3", err: errors.New("no store")},
		&sendHandler{id: "ext4"},
		&sendHandler{id: "ext5", result: strings.Repeat("long ", 100)},
	)
	results, err := hooks.OperatorSent(context.Background(), extension.OperatorSend{Text: "yes"})
	if err == nil || !strings.Contains(err.Error(), `"ext1"`) || !strings.Contains(err.Error(), `"ext3"`) {
		t.Fatalf("err = %v, want both failures", err)
	}
	if len(results) != 2 || results[0] != (extension.OperatorSendResult{Extension: "ext2", Result: "answered"}) {
		t.Fatalf("results = %+v", results)
	}
	if results[1].Extension != "ext5" || len(results[1].Result) > 200 {
		t.Fatalf("a long result was not cut short: %+v", results[1])
	}
	var none *extension.SessionHooks
	if got, err := none.OperatorSent(context.Background(), extension.OperatorSend{}); got != nil || err != nil {
		t.Fatalf("nil hooks = %v, %v", got, err)
	}
}

// contributor and observer each carry one hook of a launcher, so a test can
// disable an extension that has no say in spawns.
type contributor struct{ l *launcher }

func (c contributor) Descriptor() extension.Descriptor     { return c.l.Descriptor() }
func (c contributor) Configure(cfg extension.Config) error { return c.l.Configure(cfg) }
func (c contributor) LaunchEnv(ctx context.Context, launch extension.Launch) (map[string]string, error) {
	return c.l.LaunchEnv(ctx, launch)
}

type observer struct{ l *launcher }

func (o observer) Descriptor() extension.Descriptor     { return o.l.Descriptor() }
func (o observer) Configure(cfg extension.Config) error { return o.l.Configure(cfg) }
func (o observer) Migrated(ctx context.Context, migration extension.Migration) error {
	return o.l.Migrated(ctx, migration)
}

const refusedSpawn = `spawn refused: extension "ext1" is disabled: [extensions.ext1]: unknown key(s): bogus`

func TestADisabledSpawnPolicyRefusesEverySpawn(t *testing.T) {
	var log []string
	exts := []extension.Extension{
		&launcher{id: "extra", log: &log, env: map[string]string{"EXTRA": "1"}},
		&launcher{id: "ext1", log: &log},
		contributor{&launcher{id: "tally", log: &log, env: map[string]string{"TALLY": "1"}}},
		observer{&launcher{id: "level", log: &log}},
	}
	sections := map[string]map[string]any{
		"ext1":  {"bogus": 1},
		"tally": {"bogus": 1},
		"level": {"bogus": 1},
	}
	registry, err := extension.NewRegistry(exts)
	if err != nil {
		t.Fatal(err)
	}
	report := registry.Configure("", sections)
	if len(report.Disabled) != 3 {
		t.Fatalf("disabled %v, want ext1, tally and level", report.Disabled)
	}
	hooks, err := registry.SessionHooks("", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, by := range []extension.SpawnSource{extension.SpawnBySession, extension.SpawnByOperator, extension.SpawnByExtension} {
		err := hooks.AllowSpawn(ctx, extension.Spawn{By: by})
		if err == nil || err.Error() != refusedSpawn {
			t.Fatalf("a spawn by %s: got %v, want %q", by, err, refusedSpawn)
		}
	}
	if err := hooks.Spawned(ctx, extension.Spawn{}); err != nil {
		t.Fatal(err)
	}
	// A disabled contributor or observer is skipped, and the healthy
	// extension is still asked.
	env, err := hooks.LaunchEnv(ctx, extension.Launch{Reason: extension.LaunchRelaunch})
	if err != nil || len(env) != 1 || env["EXTRA"] != "1" {
		t.Fatalf("LaunchEnv = %v, %v; want extra's alone", env, err)
	}
	if err := hooks.Migrated(ctx, extension.Migration{}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"configure extra", "configure ext1", "configure tally", "configure level",
		"allow extra", "allow extra", "allow extra", "spawned extra", "env extra", "migrated extra",
	}
	if !slices.Equal(log, want) {
		t.Fatalf("asked %q, want %q", log, want)
	}
}

func TestADisabledSpawnPolicyRefusesOnAnUnconfiguredRegistry(t *testing.T) {
	var log []string
	registry, err := extension.NewRegistry([]extension.Extension{&launcher{id: "ext1", log: &log}})
	if err != nil {
		t.Fatal(err)
	}
	hooks, err := registry.SessionHooks(t.TempDir(), map[string]map[string]any{"ext1": {"bogus": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := hooks.AllowSpawn(context.Background(), extension.Spawn{}); err == nil || err.Error() != refusedSpawn {
		t.Fatalf("got %v, want %q", err, refusedSpawn)
	}
	if want := []string{"configure ext1"}; !slices.Equal(log, want) {
		t.Fatalf("asked %q, want %q", log, want)
	}
}

func TestHealthyPoliciesIgnoreOtherDisabledHooks(t *testing.T) {
	var log []string
	registry, err := extension.NewRegistry([]extension.Extension{
		&launcher{id: "extra", log: &log},
		contributor{&launcher{id: "tally", log: &log}},
		observer{&launcher{id: "level", log: &log}},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry.Configure("", map[string]map[string]any{"tally": {"bogus": 1}, "level": {"bogus": 1}})
	hooks, err := registry.SessionHooks("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := hooks.AllowSpawn(context.Background(), extension.Spawn{}); err != nil {
		t.Fatalf("only a disabled policy refuses spawns; got %v", err)
	}
	if want := []string{"configure extra", "configure tally", "configure level", "allow extra"}; !slices.Equal(log, want) {
		t.Fatalf("asked %q, want %q", log, want)
	}
}
