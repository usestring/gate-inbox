package extension

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// SessionHooks is what a registry's enabled extensions have to say about
// sessions being launched: the spawn shapers, spawn policies, launch
// contributors and migration observers, each asked in registration order. The host asks it
// at every launch; extensions never see it.
//
// A nil SessionHooks has nothing to say: it allows every spawn and adds no
// environment.
type SessionHooks struct {
	policies     []owned[SpawnPolicy]
	contributors []owned[LaunchContributor]
	observers    []owned[MigrationObserver]
	shapers      []owned[SpawnShaper]
}

type owned[T any] struct {
	id string
	v  T
}

// SessionHooks collects the enabled extensions that implement SpawnShaper,
// SpawnPolicy, LaunchContributor or MigrationObserver. On a registry nothing has
// configured yet only those are configured, from sections and under
// configDir, as AccountPool does: a CLI command launching one session has
// no use for the rest, and must not fail on a section it never reads.
func (r *Registry) SessionHooks(configDir string, sections map[string]map[string]any) (*SessionHooks, error) {
	var found []int
	for i, ext := range r.extensions {
		_, policy := ext.(SpawnPolicy)
		_, contributor := ext.(LaunchContributor)
		_, observer := ext.(MigrationObserver)
		_, shaper := ext.(SpawnShaper)
		if policy || contributor || observer || shaper {
			found = append(found, i)
		}
	}
	if !r.configured {
		if err := r.configureOnly(configDir, sections, found); err != nil {
			return nil, err
		}
	}
	hooks := &SessionHooks{}
	for _, i := range found {
		ext := r.extensions[i]
		if !enabled(ext) {
			continue
		}
		id := r.ids[i]
		if v, ok := ext.(SpawnPolicy); ok {
			hooks.policies = append(hooks.policies, owned[SpawnPolicy]{id, v})
		}
		if v, ok := ext.(LaunchContributor); ok {
			hooks.contributors = append(hooks.contributors, owned[LaunchContributor]{id, v})
		}
		if v, ok := ext.(MigrationObserver); ok {
			hooks.observers = append(hooks.observers, owned[MigrationObserver]{id, v})
		}
		if v, ok := ext.(SpawnShaper); ok {
			hooks.shapers = append(hooks.shapers, owned[SpawnShaper]{id, v})
		}
	}
	return hooks, nil
}

// ShapeSpawn asks every shaper, and stops at the first that refuses. The
// prefixes are joined in registration order, each followed by a blank line,
// and the launch is kept under its spawner when any shaper asks for it.
func (h *SessionHooks) ShapeSpawn(ctx context.Context, launch Launch) (SpawnShape, error) {
	var shape SpawnShape
	if h == nil {
		return shape, nil
	}
	var prefixes []string
	for _, s := range h.shapers {
		var got SpawnShape
		err := guard(func() error {
			var err error
			got, err = s.v.ShapeSpawn(ctx, launch)
			return err
		})
		if err != nil {
			return SpawnShape{}, fmt.Errorf("extension %q refused the launch: %w", s.id, err)
		}
		if prefix := strings.TrimSpace(got.PromptPrefix); prefix != "" {
			prefixes = append(prefixes, prefix)
		}
		shape.KeepUnderSpawner = shape.KeepUnderSpawner || got.KeepUnderSpawner
	}
	shape.PromptPrefix = strings.Join(prefixes, "\n\n")
	return shape, nil
}

// Prefixed is prompt with the shape's prefix ahead of it.
func (s SpawnShape) Prefixed(prompt string) string {
	prefix := strings.TrimSpace(s.PromptPrefix)
	if prefix == "" {
		return prompt
	}
	if prompt == "" {
		return prefix
	}
	return prefix + "\n\n" + prompt
}

// AllowSpawn asks every policy, and stops at the first that refuses.
func (h *SessionHooks) AllowSpawn(ctx context.Context, spawn Spawn) error {
	if h == nil {
		return nil
	}
	for _, p := range h.policies {
		err := guard(func() error { return p.v.AllowSpawn(ctx, spawn) })
		if err != nil {
			return fmt.Errorf("extension %q refused the spawn: %w", p.id, err)
		}
	}
	return nil
}

// Spawned tells every policy about a spawn that launched. What it returns
// is only for the host to log: every policy is told, whatever an earlier
// one did.
func (h *SessionHooks) Spawned(ctx context.Context, spawn Spawn) error {
	if h == nil {
		return nil
	}
	var errs []error
	for _, p := range h.policies {
		if err := guard(func() error { p.v.Spawned(ctx, spawn); return nil }); err != nil {
			errs = append(errs, fmt.Errorf("extension %q: %w", p.id, err))
		}
	}
	return errors.Join(errs...)
}

// LaunchEnv is every contributor's environment for launch, merged. A name
// that is not a shell variable, or one two contributors both set, refuses
// the launch; which names the host keeps for itself is the host's to check.
func (h *SessionHooks) LaunchEnv(ctx context.Context, launch Launch) (map[string]string, error) {
	if h == nil {
		return nil, nil
	}
	var env map[string]string
	setBy := map[string]string{}
	for _, c := range h.contributors {
		var got map[string]string
		err := guard(func() error {
			var err error
			got, err = c.v.LaunchEnv(ctx, launch)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("extension %q refused the launch: %w", c.id, err)
		}
		keys := make([]string, 0, len(got))
		for key := range got {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if !envName(key) {
				return nil, fmt.Errorf("extension %q set %q, which is not an environment variable name", c.id, key)
			}
			if other, taken := setBy[key]; taken {
				return nil, fmt.Errorf("extensions %q and %q both set %s", other, c.id, key)
			}
			if env == nil {
				env = map[string]string{}
			}
			env[key] = got[key]
			setBy[key] = c.id
		}
	}
	return env, nil
}

// Migrated tells every observer about a migration, and stops at the first
// that fails: the host undoes the migration then, so telling the rest about
// a session that is about to go would be telling them something false.
func (h *SessionHooks) Migrated(ctx context.Context, migration Migration) error {
	if h == nil {
		return nil
	}
	for _, o := range h.observers {
		if err := guard(func() error { return o.v.Migrated(ctx, migration) }); err != nil {
			return fmt.Errorf("extension %q could not follow the migration: %w", o.id, err)
		}
	}
	return nil
}

// guard turns a panic into an error, so a broken extension refuses rather
// than taking the launching process down with it.
func guard(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panicked: %v", recovered)
		}
	}()
	return fn()
}

func envName(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !(c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || i > 0 && '0' <= c && c <= '9') {
			return false
		}
	}
	return true
}
