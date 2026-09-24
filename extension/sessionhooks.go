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
// sessions being launched: the spawn shapers, spawn policies, launch
// contributors, migration observers, new-session form fields and form
// spawn observers, each asked in registration order, and the roles that
// change how the board treats a session. The host asks it at every
// launch; extensions never see it.
//
// A nil SessionHooks has nothing to say: it allows every spawn, adds no
// environment, and knows no role.
type SessionHooks struct {
	policies     []policy
	contributors []owned[LaunchContributor]
	observers    []owned[MigrationObserver]
	// roles is every provider's specs by qualified name, and relayers the
	// providers that vet their relays, by extension ID.
	roles      map[string]RoleSpec
	relayers   map[string]Relayer
	shapers    []owned[SpawnShaper]
	killers    []owned[KillObserver]
	senders    []owned[OperatorSendHandler]
	forms      []owned[SessionFormExtender]
	formSpawns []owned[FormSpawnObserver]
}

type owned[T any] struct {
	id string
	v  T
}

// policy is one spawn policy. off is set when its extension's config
// section was refused: the policy is never asked, and every spawn it would
// have been asked about is refused with off.
type policy struct {
	id  string
	v   SpawnPolicy
	off error
}

// SessionHooks collects the enabled extensions that implement SpawnShaper,
// SpawnPolicy, LaunchContributor, MigrationObserver, RoleProvider,
// KillObserver, OperatorSendHandler, SessionFormExtender or
// FormSpawnObserver. On a registry nothing has
// configured yet only those are configured, from sections and under
// configDir, as AccountPool does: a CLI command launching one session has
// no use for the rest, and must not fail on a section it never reads.
//
// An extension whose section was refused is disabled, and its hooks are
// left out -- except a SpawnPolicy, which fails closed: it stays in the
// order it was registered in, is never asked, and refuses every spawn in
// its place. A policy is there to hold spawns back, so a broken config
// must not quietly lift it.
func (r *Registry) SessionHooks(configDir string, sections map[string]map[string]any) (*SessionHooks, error) {
	var found []int
	for i, ext := range r.extensions {
		_, policy := ext.(SpawnPolicy)
		_, contributor := ext.(LaunchContributor)
		_, observer := ext.(MigrationObserver)
		_, roles := ext.(RoleProvider)
		_, shaper := ext.(SpawnShaper)
		_, killer := ext.(KillObserver)
		_, sender := ext.(OperatorSendHandler)
		_, form := ext.(SessionFormExtender)
		_, formSpawn := ext.(FormSpawnObserver)
		if policy || contributor || observer || roles || shaper || killer || sender || form || formSpawn {
			found = append(found, i)
		}
	}
	// A section refused switches that extension's hooks off and no one
	// else's, as it does its tools -- except a spawn policy, which fails
	// closed: it stays in its place, is never asked, and refuses every
	// spawn it would have been asked about.
	disabled := r.configureOnly(found, configDir, sections)
	hooks := &SessionHooks{}
	for _, i := range found {
		ext := r.extensions[i]
		id := r.ids[i]
		if err := disabled[id]; err != nil {
			if v, ok := ext.(SpawnPolicy); ok {
				hooks.policies = append(hooks.policies, policy{id: id, v: v, off: disabledError(id, err)})
			}
			continue
		}
		if !enabled(ext) {
			continue
		}
		if v, ok := ext.(SpawnPolicy); ok {
			hooks.policies = append(hooks.policies, policy{id: id, v: v})
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
		if v, ok := ext.(SpawnShaper); ok {
			hooks.shapers = append(hooks.shapers, owned[SpawnShaper]{id, v})
		}
		if v, ok := ext.(KillObserver); ok {
			hooks.killers = append(hooks.killers, owned[KillObserver]{id, v})
		}
		if v, ok := ext.(OperatorSendHandler); ok {
			hooks.senders = append(hooks.senders, owned[OperatorSendHandler]{id, v})
		}
		if v, ok := ext.(SessionFormExtender); ok {
			hooks.forms = append(hooks.forms, owned[SessionFormExtender]{id, v})
		}
		if v, ok := ext.(FormSpawnObserver); ok {
			hooks.formSpawns = append(hooks.formSpawns, owned[FormSpawnObserver]{id, v})
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

// ShapeSpawn asks every shaper, and stops at the first that refuses. The
// prefixes and the suffixes are each joined in registration order, a blank
// line apart, and the launch is kept under its spawner when any shaper asks
// for it.
func (h *SessionHooks) ShapeSpawn(ctx context.Context, launch Launch) (SpawnShape, error) {
	var shape SpawnShape
	if h == nil {
		return shape, nil
	}
	var prefixes, suffixes []string
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
		if suffix := strings.TrimSpace(got.PromptSuffix); suffix != "" {
			suffixes = append(suffixes, suffix)
		}
		shape.KeepUnderSpawner = shape.KeepUnderSpawner || got.KeepUnderSpawner
	}
	shape.PromptPrefix = strings.Join(prefixes, "\n\n")
	shape.PromptSuffix = strings.Join(suffixes, "\n\n")
	return shape, nil
}

// Shaped is prompt with the shape's prefix ahead of it and its suffix after
// it, each a blank line away.
func (s SpawnShape) Shaped(prompt string) string {
	prompt = s.Prefixed(prompt)
	suffix := strings.TrimSpace(s.PromptSuffix)
	if suffix == "" {
		return prompt
	}
	if prompt == "" {
		return suffix
	}
	return prompt + "\n\n" + suffix
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

// AllowSpawn asks every policy, and stops at the first that refuses. A
// policy its config disabled refuses without being asked, naming the
// extension and why its section was refused.
func (h *SessionHooks) AllowSpawn(ctx context.Context, spawn Spawn) error {
	if h == nil {
		return nil
	}
	for _, p := range h.policies {
		if p.off != nil {
			return fmt.Errorf("spawn refused: %w", p.off)
		}
		err := guard(func() error { return p.v.AllowSpawn(ctx, spawn) })
		if err != nil {
			return fmt.Errorf("extension %q refused the spawn: %w", p.id, err)
		}
	}
	return nil
}

// Spawned tells every policy about a spawn that launched. What it returns
// is only for the host to log: every policy is told, whatever an earlier
// one did. A disabled policy is not told.
func (h *SessionHooks) Spawned(ctx context.Context, spawn Spawn) error {
	if h == nil {
		return nil
	}
	var errs []error
	for _, p := range h.policies {
		if p.off != nil {
			continue
		}
		if err := guard(func() error { p.v.Spawned(ctx, spawn); return nil }); err != nil {
			errs = append(errs, fmt.Errorf("extension %q: %w", p.id, err))
		}
	}
	return errors.Join(errs...)
}

// FormFields is every enabled extension's new-session form fields, in
// registration order, made whole: each Key qualified as "<extension id>/<key>",
// the label filled in, a toggle's options set and the default one of the
// options. A field that is not well formed is left out, and the error names
// it; the rest are returned with the error.
func (h *SessionHooks) FormFields() ([]FormField, error) {
	if h == nil {
		return nil, nil
	}
	var fields []FormField
	var errs []error
	for _, f := range h.forms {
		var declared []FormField
		if err := guard(func() error { declared = f.v.SessionFormFields(); return nil }); err != nil {
			errs = append(errs, fmt.Errorf("extension %q: form fields: %w", f.id, err))
			continue
		}
		seen := map[string]bool{}
		for _, field := range declared {
			whole, err := wholeField(field)
			if err == nil && seen[field.Key] {
				err = errors.New("given twice")
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("extension %q: form field %q: %w", f.id, field.Key, err))
				continue
			}
			seen[field.Key] = true
			whole.Key = f.id + "/" + field.Key
			fields = append(fields, whole)
		}
	}
	return fields, errors.Join(errs...)
}

func wholeField(field FormField) (FormField, error) {
	if field.Key == "" || strings.Contains(field.Key, "/") {
		return field, errors.New("a key must be set and have no \"/\"")
	}
	if field.Label == "" {
		field.Label = field.Key
	}
	switch field.Kind {
	case FormToggle:
		field.Options = []string{FormOff, FormOn}
	case FormChoice:
		if len(field.Options) == 0 {
			return field, errors.New("a choice needs options")
		}
		field.Options = slices.Clone(field.Options)
	default:
		return field, fmt.Errorf("unknown kind %q", field.Kind)
	}
	if !slices.Contains(field.Options, field.Default) {
		field.Default = field.Options[0]
	}
	return field, nil
}

// ownForm is the part of a form's values that one extension added, under
// its own keys; nil when it added none, or the launch was not from the form.
func ownForm(form map[string]string, id string) map[string]string {
	var own map[string]string
	for key, value := range form {
		if rest, ok := strings.CutPrefix(key, id+"/"); ok {
			if own == nil {
				own = map[string]string{}
			}
			own[rest] = value
		}
	}
	return own
}

// FormSpawned tells each form spawn observer whose fields were on the form
// that sess was spawned from it, handing it only its own values, under its
// own keys, in registration order. form is keyed as FormFields keys its
// fields; an observer with no values in it is not called. What it returns
// is only for the host to log: every observer is told, whatever an earlier
// one did.
func (h *SessionHooks) FormSpawned(ctx context.Context, sess SessionInfo, form map[string]string) error {
	if h == nil {
		return nil
	}
	var errs []error
	for _, o := range h.formSpawns {
		own := ownForm(form, o.id)
		if own == nil {
			continue
		}
		if err := guard(func() error { o.v.FormSpawned(ctx, sess, own); return nil }); err != nil {
			errs = append(errs, fmt.Errorf("extension %q: %w", o.id, err))
		}
	}
	return errors.Join(errs...)
}

// LaunchEnv is every contributor's environment for launch, merged. A name
// that is not a shell variable, or one two contributors both set, refuses
// the launch; which names the host keeps for itself is the host's to check.
//
// launch.Form is keyed as FormFields keys its fields. Each contributor is
// handed only the values of its own fields, under its own keys.
func (h *SessionHooks) LaunchEnv(ctx context.Context, launch Launch) (map[string]string, error) {
	if h == nil {
		return nil, nil
	}
	var env map[string]string
	setBy := map[string]string{}
	for _, c := range h.contributors {
		var got map[string]string
		own := launch
		own.Form = ownForm(launch.Form, c.id)
		err := guard(func() error {
			var err error
			got, err = c.v.LaunchEnv(ctx, own)
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

// Killed tells every kill observer about a kill. What it returns is only
// for the host to log: every observer is told, whatever an earlier one did.
func (h *SessionHooks) Killed(ctx context.Context, kill KillContext) error {
	if h == nil {
		return nil
	}
	var errs []error
	for _, k := range h.killers {
		if err := guard(func() error { k.v.Killed(ctx, kill); return nil }); err != nil {
			errs = append(errs, fmt.Errorf("extension %q: %w", k.id, err))
		}
	}
	return errors.Join(errs...)
}

// maxSendResultBytes bounds what one extension can add to a sender's
// answer: a word or a phrase, not a report.
const maxSendResultBytes = 200

// OperatorSent tells every handler about an operator's send, in order, and
// returns the non-empty results. A handler's error or panic does not stop
// the rest, and comes back joined for the host to log.
func (h *SessionHooks) OperatorSent(ctx context.Context, send OperatorSend) ([]OperatorSendResult, error) {
	if h == nil {
		return nil, nil
	}
	var results []OperatorSendResult
	var errs []error
	for _, s := range h.senders {
		var got string
		err := guard(func() error {
			var err error
			got, err = s.v.OperatorSent(ctx, send)
			return err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("extension %q: %w", s.id, err))
			continue
		}
		got = strings.Join(strings.Fields(got), " ")
		if len(got) > maxSendResultBytes {
			got = strings.ToValidUTF8(got[:maxSendResultBytes], "")
		}
		if got != "" {
			results = append(results, OperatorSendResult{Extension: s.id, Result: got})
		}
	}
	return results, errors.Join(errs...)
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
