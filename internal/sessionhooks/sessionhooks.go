// Package sessionhooks is how a launching process finds what its build's
// extensions have to say about sessions: which spawns they allow, what
// environment they add, which migrations they follow, which kills they
// hear of. Every process that
// launches a session asks here -- the board, a session's CLI, its MCP
// server -- so a build's policy binds whichever of them launches.
package sessionhooks

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// Timeout bounds one question put to the extensions while a launch waits.
const Timeout = 30 * time.Second

var (
	mu       sync.Mutex
	resolve  = func() (*extension.SessionHooks, error) { return nil, nil }
	sessions = extension.SessionReader(unread{})
)

// errUnread is a process that was never told how to read its board.
var errUnread = errors.New("this process has no board to read sessions from")

// unread is the reader of a process nothing has given one: every read
// fails, so a policy counting on one refuses rather than counting nothing.
type unread struct{}

func (unread) Get(context.Context, string) (extension.SessionInfo, error) {
	return extension.SessionInfo{}, errUnread
}

func (unread) List(context.Context, extension.SessionFilter) (extension.SessionList, error) {
	return extension.SessionList{}, errUnread
}

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

// UseSessions sets what a spawn policy reads the board through in this
// process. It returns a func restoring the previous reader, for tests.
func UseSessions(r extension.SessionReader) (restore func()) {
	mu.Lock()
	defer mu.Unlock()
	previous := sessions
	sessions = r
	return func() {
		mu.Lock()
		defer mu.Unlock()
		sessions = previous
	}
}

func reader() extension.SessionReader {
	mu.Lock()
	defer mu.Unlock()
	return sessions
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

		ReplacedBy: sess.ReplacedBy,
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

// Shape asks this process's spawn shapers how sess, which is about to be
// launched for reason, should start. from is the source session of a
// migration.
func Shape(sess store.Session, reason extension.LaunchReason, from string) (extension.SpawnShape, error) {
	hooks, err := Current()
	if err != nil {
		return extension.SpawnShape{}, err
	}
	ctx, cancel := bounded()
	defer cancel()
	return hooks.ShapeSpawn(ctx, extension.Launch{Session: Info(sess, false), Reason: reason, From: from})
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
	if err := hooks.AllowSpawn(ctx, extension.Spawn{Session: Info(sess, false), By: by, Sessions: reader()}); err != nil {
		return nil, err
	}
	return hooks, nil
}

// Spawned tells the policies that sess launched. A policy that fails to
// hear it is logged: the session exists either way.
func Spawned(hooks *extension.SessionHooks, sess store.Session, by extension.SpawnSource) {
	ctx, cancel := bounded()
	defer cancel()
	if err := hooks.Spawned(ctx, extension.Spawn{Session: Info(sess, true), By: by, Sessions: reader()}); err != nil {
		logging.Warn("an extension failed to hear of a spawn", "session", sess.ID, "err", err)
	}
}

// FormSpawned tells the extensions that sess, now running, was spawned from
// the new-session form with form's values, keyed as FormFields keys them.
// An extension that fails to hear it is logged: the session exists either
// way.
func FormSpawned(sess store.Session, form map[string]string) {
	if len(form) == 0 {
		return
	}
	hooks, err := Current()
	if err != nil {
		logging.Warn("the extensions could not be told of a spawn from the form", "session", sess.ID, "err", err)
		return
	}
	ctx, cancel := bounded()
	defer cancel()
	if err := hooks.FormSpawned(ctx, Info(sess, true), form); err != nil {
		logging.Warn("an extension failed to hear of a spawn from the form", "session", sess.ID, "err", err)
	}
}

// Env is what the extensions add to the environment sess is launched with.
// from is the source session of a migration, and form the values of the
// extensions' new-session form fields when the operator spawned it from
// that form, keyed as FormFields keys them.
func Env(hooks *extension.SessionHooks, sess store.Session, reason extension.LaunchReason, from string, form map[string]string) (map[string]string, error) {
	ctx, cancel := bounded()
	defer cancel()
	return hooks.LaunchEnv(ctx, extension.Launch{Session: Info(sess, false), Reason: reason, From: from, Form: form})
}

// FormFields is the fields this process's extensions add to the new-session
// form. One that could not be read is logged and left off: the form opens
// either way.
func FormFields() []extension.FormField {
	hooks, err := Current()
	if err != nil {
		logging.Warn("the extensions' form fields could not be read", "err", err)
		return nil
	}
	fields, err := hooks.FormFields()
	if err != nil {
		logging.Warn("an extension's form field was left off the form", "err", err)
	}
	return fields
}

// Migrated tells the observers that to carries on from's conversation. An
// error is the caller's to undo.
func Migrated(hooks *extension.SessionHooks, from, to store.Session, fromRunning bool) error {
	ctx, cancel := bounded()
	defer cancel()
	return hooks.Migrated(ctx, extension.Migration{From: Info(from, fromRunning), To: Info(to, true)})
}

// Killed tells the kill observers that sess, now dead, was killed via by
// the session caller (empty for none). An observer that fails to hear it is
// logged: the session is dead either way. A process whose hooks cannot be
// found logs that too, rather than failing a kill that already happened.
func Killed(sess store.Session, via extension.KillSource, caller string) {
	hooks, err := Current()
	if err != nil {
		logging.Warn("could not tell the extensions of a kill", "session", sess.ID, "err", err)
		return
	}
	ctx, cancel := bounded()
	defer cancel()
	if err := hooks.Killed(ctx, extension.KillContext{Session: Info(sess, false), Via: via, By: caller}); err != nil {
		logging.Warn("an extension failed to hear of a kill", "session", sess.ID, "err", err)
	}
}

// OperatorSent tells the handlers that the operator sent target, a running
// session, the queued message id from a shell, and returns what they made of
// it. A handler that fails is logged: the message is queued either way.
func OperatorSent(target store.Session, id int64, text, subject string, interrupt bool, at time.Time) []extension.OperatorSendResult {
	hooks, err := Current()
	if err != nil {
		logging.Warn("could not tell the extensions of an operator's send", "session", target.ID, "err", err)
		return nil
	}
	ctx, cancel := bounded()
	defer cancel()
	results, err := hooks.OperatorSent(ctx, extension.OperatorSend{
		Session:   Info(target, true),
		MessageID: id,
		Text:      text,
		Subject:   subject,
		Interrupt: interrupt,
		At:        at,
	})
	if err != nil {
		logging.Warn("an extension failed to hear of an operator's send", "session", target.ID, "message", id, "err", err)
	}
	return results
}
