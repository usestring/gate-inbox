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
