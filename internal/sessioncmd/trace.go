package sessioncmd

import (
	"time"

	"github.com/usestring/gate-inbox/internal/tracing"
)

// The commands in this package are what an operator or an agent is waiting on
// when they press a key that does something: each one loads the config, opens
// the database, reaches a tmux server, and several of them launch a process.
// None of that is on the poll pass, so none of it shows up in the pass's own
// trace, and a create that took four seconds has until now been four seconds
// of nothing.
//
// The unit is the command, not the step. A span per helper here would be a
// waterfall of function calls nobody can act on, while the command is the
// thing a person names when they say the board was slow.

// op is one command being timed. The zero value records nothing, which is what
// an untraced run gets: the check is made once where the command starts rather
// than at each of its dozen returns.
type op struct {
	name  string
	start time.Time
	attrs []tracing.Attr
}

// start opens a span for a command. Attributes name what the command acted on
// -- a session id, a tool, a target -- and never what was said to it: the
// messages, prompts and pane text these commands carry are the operator's and
// the agents' own words, and a span is shipped off the machine.
func start(name string, attrs ...tracing.Attr) op {
	if !tracing.Enabled() {
		return op{}
	}
	return op{name: name, start: time.Now(), attrs: attrs}
}

// done closes the span, reading the error through a pointer so a command can
// end it in one deferred line before it knows how it went:
//
//	defer start("sessioncmd.kill").done(&err)
func (o op) done(err *error, attrs ...tracing.Attr) {
	if o.name == "" {
		return
	}
	var failed error
	if err != nil {
		failed = *err
	}
	tracing.Record(o.name, o.start, time.Now(), failed, append(o.attrs, attrs...)...)
}

// sessionAttr is the attribute nearly every command carries: which session it
// was about. An id, because that is what the board, the log and the store
// already key on, and because a name is written by an agent.
//
// Not called session, though that is what it says: half the files here already
// use that word for a local, and a helper a scope can hide is one somebody
// will trip over.
func sessionAttr(id string) tracing.Attr { return tracing.Attr{Key: "session", Value: id} }
