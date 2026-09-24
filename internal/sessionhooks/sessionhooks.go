// Package sessionhooks is how a launching process finds what its build's
// extensions have to say about sessions: which spawns they allow, what
// environment they add, which migrations they follow. Every process that
// launches a session asks here -- the board, a session's CLI, its MCP
// server -- so a build's policy binds whichever of them launches.
package sessionhooks

import (
	"context"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// Timeout bounds one question put to the extensions while a launch waits.
const Timeout = 30 * time.Second

var (
	mu      sync.Mutex
	resolve = func() (*extension.SessionHooks, error) { return nil, nil }
)

// Use sets how this process finds its hooks: resolve is called at each
// launch, and returns nil when the build has none. It returns a func
// restoring the previous resolver, for tests.
func Use(fn func() (*extension.SessionHooks, error)) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	previous := resolve
	resolve = fn
	return func() {
		mu.Lock()
		defer mu.Unlock()
		resolve = previous
	}
}

// Current is this process's hooks. A nil result with no error has nothing to
// say, and every method on it is a no-op.
func Current() (*extension.SessionHooks, error) {
	mu.Lock()
	fn := resolve
	mu.Unlock()
	return fn()
}

func bounded() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), Timeout)
}

// Info is a row as the extensions see it. running is whether it has a live
// pane; a row about to be launched has none yet.
func Info(sess store.Session, running bool) extension.SessionInfo {
	return extension.SessionInfo{
		ID:        sess.ID,
		Name:      sess.Name,
		Tool:      sess.Tool,
		Model:     sess.Model,
		Group:     sess.Group,
		Directory: sess.Cwd,
		Status:    sess.Status,
		Running:   running,
		Archived:  sess.Archived,
		ParentID:  sess.ParentID,
		SpawnedBy: store.SpawnerOf(sess),
		Role:      sess.Role,
	}
}

// Role is how the board treats a session with role: the zero spec, an
// ordinary child, for the empty role and for one no extension declares. A
// build whose hooks will not resolve treats every role as ordinary and says
// so in the log: a question asked for every drawn row has nowhere better to
// put the error, and every launch, which resolves the same hooks, reports
// it where it can be acted on.
func Role(role string) extension.RoleSpec {
	if role == "" {
		return extension.RoleSpec{}
	}
	hooks, err := Current()
	if err != nil {
		logging.Warn("could not read the extensions' roles", "role", role, "err", err)
		return extension.RoleSpec{}
	}
	spec, _ := hooks.Role(role)
	return spec
}

// Relay puts from's send of text to to, the session it is filed under,
// through the extension that owns from's role, and returns what to queue as
// the operator's words.
func Relay(hooks *extension.SessionHooks, from, to store.Session, fromRunning, toRunning bool, text string) (string, error) {
	ctx, cancel := bounded()
	defer cancel()
	return hooks.Relay(ctx, extension.Relay{From: Info(from, fromRunning), To: Info(to, toRunning), Text: text})
}

// CheckSpawn asks this process's spawn policies about sess, which is about
// to be launched, and returns the hooks the rest of the launch goes on
// asking.
func CheckSpawn(sess store.Session, by extension.SpawnSource) (*extension.SessionHooks, error) {
	hooks, err := Current()
	if err != nil {
		return nil, err
	}
	ctx, cancel := bounded()
	defer cancel()
	if err := hooks.AllowSpawn(ctx, extension.Spawn{Session: Info(sess, false), By: by}); err != nil {
		return nil, err
	}
	return hooks, nil
}

// Spawned tells the policies that sess launched. A policy that fails to
// hear it is logged: the session exists either way.
func Spawned(hooks *extension.SessionHooks, sess store.Session, by extension.SpawnSource) {
	ctx, cancel := bounded()
	defer cancel()
	if err := hooks.Spawned(ctx, extension.Spawn{Session: Info(sess, true), By: by}); err != nil {
		logging.Warn("an extension failed to hear of a spawn", "session", sess.ID, "err", err)
	}
}

// Env is what the extensions add to the environment sess is launched with.
// from is the source session of a migration.
func Env(hooks *extension.SessionHooks, sess store.Session, reason extension.LaunchReason, from string) (map[string]string, error) {
	ctx, cancel := bounded()
	defer cancel()
	return hooks.LaunchEnv(ctx, extension.Launch{Session: Info(sess, false), Reason: reason, From: from})
}

// Migrated tells the observers that to carries on from's conversation. An
// error is the caller's to undo.
func Migrated(hooks *extension.SessionHooks, from, to store.Session, fromRunning bool) error {
	ctx, cancel := bounded()
	defer cancel()
	return hooks.Migrated(ctx, extension.Migration{From: Info(from, fromRunning), To: Info(to, true)})
}
