package extension

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// SessionHooks is what a registry's enabled extensions have to say about
// sessions being launched: the spawn policies, launch contributors and
// migration observers, each asked in registration order, and the roles
// that change how the board treats a session. The host asks it at every
// launch; extensions never see it.
//
// A nil SessionHooks has nothing to say: it allows every spawn, adds no
// environment, and knows no role.
type SessionHooks struct {
	policies     []owned[SpawnPolicy]
	contributors []owned[LaunchContributor]
	observers    []owned[MigrationObserver]
	// roles is every provider's specs by qualified name, and relayers the
	// providers that vet their relays, by extension ID.
	roles    map[string]RoleSpec
	relayers map[string]Relayer
}

type owned[T any] struct {
	id string
	v  T
}

// SessionHooks collects the enabled extensions that implement SpawnPolicy,
// LaunchContributor, MigrationObserver or RoleProvider. On a registry nothing has
// configured yet only those are configured, from sections and under
// configDir, as AccountPool does: a CLI command launching one session has
// no use for the rest, and must not fail on a section it never reads.
func (r *Registry) SessionHooks(configDir string, sections map[string]map[string]any) (*SessionHooks, error) {
	var found []int
	for i, ext := range r.extensions {
		_, policy := ext.(SpawnPolicy)
		_, contributor := ext.(LaunchContributor)
		_, observer := ext.(MigrationObserver)
		_, roles := ext.(RoleProvider)
		if policy || contributor || observer || roles {
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
		if v, ok := ext.(RoleProvider); ok {
			if err := hooks.addRoles(id, v); err != nil {
				return nil, err
			}
		}
	}
	return hooks, nil
}

// rolePattern is the shape of a role's unqualified name. The host checks
// LaunchRequest.Role against the same one, so every spec names a role that
// can be launched.
var rolePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// addRoles qualifies provider's specs with id. A malformed or repeated name
// is the build's mistake, and is refused rather than read one way or the
// other.
func (h *SessionHooks) addRoles(id string, provider RoleProvider) error {
	var specs []RoleSpec
	if err := guard(func() error { specs = provider.Roles(); return nil }); err != nil {
		return fmt.Errorf("extension %q could not list its roles: %w", id, err)
	}
	for _, spec := range specs {
		if !rolePattern.MatchString(spec.Name) {
			return fmt.Errorf("extension %q declares role %q; a role must be lower case, start with a letter, and hold only letters, digits, '-' and '_'", id, spec.Name)
		}
		qualified := id + "/" + spec.Name
		if _, taken := h.roles[qualified]; taken {
			return fmt.Errorf("extension %q declares role %q twice", id, spec.Name)
		}
		if h.roles == nil {
			h.roles = map[string]RoleSpec{}
		}
		spec.Name = qualified
		h.roles[qualified] = spec
	}
	if relayer, ok := provider.(Relayer); ok {
		if h.relayers == nil {
			h.relayers = map[string]Relayer{}
		}
		h.relayers[id] = relayer
	}
	return nil
}

// Role is the spec for a session's role as its row records it, qualified
// with the ID of the extension that launched it; the spec's Name is that
// qualified role. The empty role, and one no enabled extension declares,
// are an ordinary child: the zero RoleSpec, and false.
func (h *SessionHooks) Role(role string) (RoleSpec, bool) {
	if h == nil || role == "" {
		return RoleSpec{}, false
	}
	spec, ok := h.roles[role]
	return spec, ok
}

// Relay puts a relayed send to the Relayer of the extension that owns the
// sender's role, and returns what to queue as the operator's words. With no
// Relayer the text goes as it was sent. An error, or a panic, refuses the
// send.
func (h *SessionHooks) Relay(ctx context.Context, relay Relay) (deliver string, err error) {
	if h == nil {
		return relay.Text, nil
	}
	owner, _, _ := strings.Cut(relay.From.Role, "/")
	relayer, ok := h.relayers[owner]
	if !ok {
		return relay.Text, nil
	}
	err = guard(func() error {
		var err error
		deliver, err = relayer.Relay(ctx, relay)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("extension %q refused the relay: %w", owner, err)
	}
	return deliver, nil
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
