package extension

import (
	"context"
	"time"
)

// BoardProvider is implemented by an extension that runs for as long as the
// interactive board does: a loop that watches sessions and acts on them,
// rather than tools a session calls.
//
// StartBoard is called once, after Configure and before the board's first
// poll pass, so a subscription made here sees every transition the board
// observes. It must return promptly: whatever runs for the life of the board
// runs on a goroutine the extension starts here. ctx is cancelled when the
// board exits, and stop, when not nil, is called then too, before the
// board's store and tmux connections are closed.
//
// An error, or a panic, is not the board failing: whatever the extension
// subscribed is removed again, so a failed extension is indistinguishable
// from a switched-off one, and the board starts without it.
//
// Only the board runs this. A CLI command and a session's MCP server never
// start a board, so a provider's loop runs at most once per config
// directory, in the one process the singleton lock lets through.
type BoardProvider interface {
	StartBoard(ctx context.Context, board BoardHost) (stop func(), err error)
}

// BoardHost is what the board lends a BoardProvider: the Board, with the
// operator's reach over reads and answers, sessions of its own to launch,
// and what the board observes while it polls. Every callback is delivered on
// a goroutine of the subscription's own, in the order the board observed
// them, never on the poll loop: a subscriber that is slow delays only its
// own later deliveries, and one that panics is logged and kept.
type BoardHost interface {
	Board
	// Subscribe calls fn with every status transition a poll pass writes,
	// until unsubscribe is called. It is edge-triggered: a session that was
	// already waiting when the board started produces no event, and OnPass
	// is how a subscriber finds it.
	Subscribe(fn func(StatusEvent)) (unsubscribe func())
	// OnPass calls fn after each poll pass with every unarchived session's
	// status as the pass left it. A subscriber still busy with an earlier
	// pass is handed only the newest one when it is done: a pass is a
	// level, and a stale one says nothing the newest does not.
	OnPass(fn func(Pass)) (unsubscribe func())
	// Launch starts an agent session for the extension, as the operator
	// would from the board rather than as any session's spawn: a helper
	// filed under the session it works for, tagged with the role it plays.
	// Spawn policies are asked, with SpawnByExtension, and launch
	// contributors add their environment, as for any other spawn.
	Launch(ctx context.Context, req LaunchRequest) (SessionInfo, error)
	// Send queues a message for an agent session, delivered the way
	// send_session delivers one: at the session's next prompt, or first
	// stopping its turn when Interrupt is set. It is held to send_session's
	// checks -- no terminal, nothing archived or not running, no interrupt
	// for a tool with no way to stop a turn -- and arrives fenced as this
	// extension's, never as a person's words or another session's.
	Send(ctx context.Context, id string, msg Message) (Sent, error)
	// Kill ends an agent session's pane and leaves its row dead, as
	// kill_session does: the last screen is kept, and a revive can resume
	// the conversation. A terminal is refused.
	Kill(ctx context.Context, id string) (SessionInfo, error)
	// Replace starts a fresh agent session in an existing one's place and
	// ends the old one: a restart that throws a conversation away but keeps
	// the work it was doing. The new session takes the old one's parent,
	// group and place in the list, and its name and role unless req names
	// its own; it gets the messages still queued for the old one and the
	// file reservations it held. The old one is left dead with its last
	// screen kept, as Kill leaves it, so a revive can still resume it.
	//
	// From the board it is one step: no poll pass sees the two running side
	// by side, and a failure at any point leaves the old session as it was.
	//
	// req.Tool, Directory and Model default to the old session's; ParentID
	// and Group must be empty, since the place is the old one's; Role, when
	// set, is qualified as for Launch. Spawn policies are asked, with
	// SpawnByExtension, and launch contributors see LaunchReplace with From
	// naming the old session. A terminal is refused, and so is a session
	// wearing another extension's role.
	Replace(ctx context.Context, id string, req LaunchRequest) (SessionInfo, error)
}

// Message is what BoardHost.Send queues.
type Message struct {
	Text string
	// Subject labels what the message is about: a later message from this
	// extension to the same session on the same subject replaces this one
	// while it is still queued.
	Subject string
	// Interrupt stops the session's running turn before the message is
	// typed in, so it is the next turn rather than read after the step in
	// hand.
	Interrupt bool
}

// Sent is what one BoardHost.Send queued.
type Sent struct {
	MessageID int64
	// QueuePosition counts the messages waiting for the session, this one
	// included.
	QueuePosition int
	// Held says why the message is waiting rather than typed straight in,
	// or is empty when nothing holds it.
	Held string
	// Superseded counts this extension's earlier queued messages on the same
	// subject that this one replaced.
	Superseded int
}

// EventKind classifies a transition by the status it arrived at.
type EventKind string

const (
	// EventStop is a session whose turn ended with nothing asked: it is
	// now finished.
	EventStop EventKind = "stop"
	// EventAsk is a session now waiting on an answer.
	EventAsk EventKind = "ask"
	// EventError is a session now errored.
	EventError EventKind = "error"
)

// StatusEvent is one session moving from one status to another, as a poll
// pass derived and stored it. From and To are the board's statuses, the
// ones SessionInfo.Status reports. Kind is empty for a transition none of
// the kinds names -- into working, idle, starting or dead -- and those are
// delivered too.
type StatusEvent struct {
	SessionID string
	From      string
	To        string
	Kind      EventKind
	// At is when the pass that observed the transition stored it.
	At time.Time
}

// KindOf is the EventKind of a transition into status to.
func KindOf(to string) EventKind {
	switch to {
	case "finished":
		return EventStop
	case "waiting":
		return EventAsk
	case "errored":
		return EventError
	}
	return ""
}

// Pass is one poll pass as it finished.
type Pass struct {
	At       time.Time
	Sessions []SessionStatus
}

// SessionStatus is one session's status as a pass left it.
type SessionStatus struct {
	ID     string
	Status string
}
