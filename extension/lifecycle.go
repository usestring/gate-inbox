package extension

import (
	"context"
	"errors"
	"log/slog"
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
	// Logger writes into the board's own log, each line tagged with the
	// extension's id and scrubbed of credentials like the board's lines.
	Logger() *slog.Logger
	// Tracer opens spans in the board's trace, scoped to the extension.
	Tracer() Tracer
	// PinStatus sets the status the board reads for one of the extension's
	// own sessions: one whose role is this extension's and whose RoleSpec
	// has PinnedStatus, or one it supervises. For a role's session it
	// writes the session's status file and its row, so the board shows it
	// at once rather than on the next pass. A supervised session's pin is
	// held by the board instead, over whatever the session's pane and hooks
	// report, and a pass is asked for at once to store it, so subscribers
	// are told of it as of any other transition; it holds only while the
	// session's agent runs. status is one of working, waiting, finished,
	// idle or errored; "" releases the pin, so the board reads the session
	// again.
	PinStatus(ctx context.Context, id, status string) error
	// Supervise claims an agent session for the extension to watch over,
	// or, with on false, lets it go and releases its pin. What a claim
	// grants is PinStatus: a worker the extension has stopped to put a
	// question to the operator reads as waiting, though on most CLIs its
	// pane shows only a turn that ended. A session another extension
	// launched for a role, or one another extension supervises, is
	// refused. A claim lasts until it is let go or the extension stops; it
	// is the board's, not the store's, so a board that starts again holds
	// none until StartBoard claims again.
	Supervise(ctx context.Context, id string, on bool) error
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
	// for a tool with no way to stop a turn -- but its text may run to
	// MaxMessageBytes, and one longer fails with ErrMessageTooLarge. It
	// arrives fenced as this extension's, never as another session's. Only a
	// Message with AsOperator set arrives as a person's words.
	Send(ctx context.Context, id string, msg Message) (Sent, error)
	// Withdraw drops this extension's messages to an agent session that
	// are still queued: all of them, or with a subject only the ones on it.
	// It reports how many it dropped; one whose paste has already begun is
	// past taking back and is not counted. Nothing another sender queued is
	// touched.
	Withdraw(ctx context.Context, id, subject string) (int, error)
	// Kill ends an agent session's pane and leaves its row dead, as
	// kill_session does: the last screen is kept, and a revive can resume
	// the conversation. It ends a terminal the same way when some session
	// could close it -- one nested under an agent session -- and refuses
	// the operator's own terminals, nested under none.
	Kill(ctx context.Context, id string) (SessionInfo, error)
	// OnOperator calls fn with everything the operator hands a session from
	// the board, until unsubscribe is called: a line sent from the prompt
	// bar, a snippet, a line or a dialog choice entered in a focused pane,
	// and a line the operator sent from a shell with `send --as-human`,
	// once the board has typed it in. It is how an extension that put a question to the operator learns
	// that a person has answered. Only input that reached the pane is
	// reported; one the board refused, or that tmux failed to deliver, is
	// not. Delivery is Subscribe's: in order, on the subscription's own
	// goroutine.
	OnOperator(fn func(OperatorInput)) (unsubscribe func())
	// Replace starts a fresh agent session in an existing one's place and
	// ends the old one: a restart that throws a conversation away but keeps
	// the work it was doing. The new session takes the old one's parent,
	// group and place in the list, and its name and role unless req names
	// its own; it gets the messages still queued for the old one and the
	// file reservations it held. The old one is left dead with its last
	// screen kept, as Kill leaves it, so a revive can still resume it.
	//
	// Called with no options it is one step: no poll pass sees the two
	// running side by side, a failure at any point leaves the old session
	// as it was, and the handle it returns is already committed. With
	// ReplaceOptions{Hold: true} the fresh session launches while the old
	// one keeps running, and the swap waits for the handle: see
	// ReplaceHandle.
	//
	// req.Tool, Directory and Model default to the old session's; ParentID
	// and Group must be empty, since the place is the old one's; Role, when
	// set, is qualified as for Launch. Spawn policies are asked, with
	// SpawnByExtension, and launch contributors see LaunchReplace with From
	// naming the old session. A terminal is refused, and so is a session
	// wearing another extension's role. At most one ReplaceOptions may be
	// passed.
	Replace(ctx context.Context, id string, req LaunchRequest, opts ...ReplaceOptions) (SessionInfo, ReplaceHandle, error)
	// Command types one of a tool's own commands, such as "/model fast",
	// into an agent session's input line and submits it, as the operator
	// would at its prompt. Unlike Send it is neither queued nor fenced: it
	// is typed now or refused, with ErrNotAtPrompt while the session is
	// mid-turn, holding a dialog, scrolled into its history, or has a
	// person typing or a draft at its prompt, and with ErrNoCommandLine for
	// a tool whose input line the board cannot find. With Confirm set it
	// then presses Enter on the confirmation the command draws, if it draws
	// one; see ToolCommand.
	Command(ctx context.Context, id string, cmd ToolCommand) error
	// Unpark brings an agent session's viewport back to the live bottom
	// when its tool has scrolled it into history, by pressing the tool's
	// jump-back key, and reports whether it was parked. A parked pane shows
	// the session's past, so nothing read off it describes now; the key
	// takes effect by the next read. A tool with no jump-back affordance
	// configured is never parked.
	Unpark(ctx context.Context, id string) (parked bool, err error)
	// PlanReplace is what Replace would launch for the same arguments,
	// composed by the same code and refused for the same reasons, with
	// nothing done: no row filed, no pane started or ended, nothing moved,
	// no turn of an account pool taken. Spawn policies and launch
	// contributors are asked, since what they answer is part of the plan.
	// The plan's SessionID is its own; a Replace that follows mints another,
	// and that id, wherever the command and environment carry it, is the
	// only difference between the two.
	PlanReplace(ctx context.Context, id string, req LaunchRequest) (LaunchPlan, error)
	// Archive files one of this extension's own helpers -- a session it
	// launched with a role -- out of the active list, ending it first if it
	// is running, as archive_session does. Every other session is refused:
	// what the operator or a session started is theirs to file away.
	Archive(ctx context.Context, id string) error
}

// OperatorInput is one thing the operator handed a session from the board.
type OperatorInput struct {
	SessionID string
	Via       OperatorVia
	// Text is the line sent. It is empty for input typed into a focused
	// pane, where the board passes keystrokes on and never holds the line.
	Text string
	// Dialog is set when the input answered a dialog the pane was holding,
	// rather than a line at the session's prompt.
	Dialog bool
	// MessageID is the queued message's id when Via is OperatorCLI, and
	// zero otherwise. It is the MessageID OperatorSendHandler.OperatorSent
	// was given for the same send: an extension that handled the send there
	// dedupes on MessageID and skips this report of it.
	MessageID int64
	At        time.Time
}

// OperatorVia is where on the board the operator's input came from.
type OperatorVia string

const (
	// OperatorPrompt is the board's prompt bar, or the reply composer the
	// gate opens, sending a line the operator wrote.
	OperatorPrompt OperatorVia = "prompt"
	// OperatorSnippet is one of the operator's snippets, sent with its key.
	OperatorSnippet OperatorVia = "snippet"
	// OperatorPane is a key typed into a focused pane that submitted its
	// line or chose an option in its dialog.
	OperatorPane OperatorVia = "pane"
	// OperatorCLI is a line the operator sent from a shell with
	// `send --as-human`. It is reported by the board once it has typed the
	// line into the pane, exactly once per message, and never for a message
	// an agent or an extension sent. OperatorInput.MessageID names the
	// message, which OperatorSendHandler was told of at send.
	OperatorCLI OperatorVia = "cli"
)

// ToolCommand is what BoardHost.Command types.
type ToolCommand struct {
	// Text is the command: one line, starting with "/".
	Text string
	// Confirm, when set, is how the option a command's own confirmation
	// offers to go ahead begins, such as "Yes, switch to". For a moment
	// after typing, the board watches the pane for a menu whose selected
	// row begins with it and presses Enter on that row once. It never
	// answers a menu whose selected row says anything else, nor a
	// permission prompt, and it fails with ErrCommandHeld when the pane is
	// still held as the watch ends. Empty, the command is typed and
	// nothing is answered.
	Confirm string
}

var (
	// ErrNotAtPrompt is a session that cannot take a command now: it is
	// mid-turn, on a dialog, scrolled into history, or somebody is typing
	// at it. It clears on its own, so a later try may succeed.
	ErrNotAtPrompt = errors.New("the session is not resting at its prompt")
	// ErrNoCommandLine is a session whose tool declares no input line the
	// board can recognise, so nothing can tell when a command would land.
	ErrNoCommandLine = errors.New("the session's tool has no input line the board can find")
	// ErrCommandHeld is a command whose confirmation, or something else,
	// was still holding the pane when the watch after typing it ended.
	ErrCommandHeld = errors.New("the session was still held after the command")
)

// MaxMessageBytes bounds Message.Text, once trimmed. It is well over what a
// session may send another, so what an extension gathered for an agent can
// go inline rather than in a file the agent has to be told to read.
const MaxMessageBytes = 64 << 10

// ErrMessageTooLarge is a message over MaxMessageBytes, refused by
// BoardHost.Send before anything is queued.
var ErrMessageTooLarge = errors.New("message is over the size limit")

// ReplaceOptions changes how BoardHost.Replace swaps one session for
// another. The zero value is the one-step swap.
type ReplaceOptions struct {
	// Hold launches the fresh session without retiring the old one, so the
	// extension can check that the fresh one took -- that it bound to its
	// work, answered, came up at all -- before the old one is given up.
	Hold bool
}

// ReplaceHandle settles a replacement BoardHost.Replace held.
//
// During the hold the fresh session is its own row, filed as a leaf under
// the session it will replace and wearing its role: two rows for two
// running sessions, never two in one seat. The old session keeps its seat,
// its queued messages, its file reservations and its pane, and whatever is
// sent to it meanwhile is still its own until the commit forwards it.
//
// Commit makes the swap exactly as an unheld Replace would have made it:
// the fresh session moves into the old one's seat, takes the messages still
// queued for it and its file reservations, and the old one is left dead
// with its last screen. It fails, and leaves both sessions as they were,
// when the fresh one was archived or deleted meanwhile, or the old one was
// archived; and with ErrReplacementNotRunning when the fresh one was killed
// or its pane ended. A failed Commit leaves the handle held, to Abort.
//
// Abort ends the fresh session and deletes its row, as a launch that
// failed leaves nothing behind, and does not touch the old one. A handle
// the extension leaves unsettled is aborted when the extension stops,
// whether the board is exiting or the extension failed. A hold still
// unsettled when the board is killed outright is aborted the same way when
// the board next starts, before any extension does.
//
// Repeating the call that settled a handle does nothing and returns nil;
// the other call after it returns ErrReplaceSettled.
type ReplaceHandle interface {
	Commit(ctx context.Context) error
	Abort(ctx context.Context) error
}

// ErrReplaceSettled is a ReplaceHandle asked to commit after it was aborted,
// or to abort after it was committed.
var ErrReplaceSettled = errors.New("the replacement was already settled the other way")

// ErrReplacementNotRunning is a ReplaceHandle's Commit refused because the
// fresh session is dead or its pane is gone: the old session is not given
// up for it.
var ErrReplacementNotRunning = errors.New("the fresh session is no longer running")

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
	// AsOperator delivers Text as the operator's own words: typed into the
	// session's prompt with no fence, exactly as the operator's own send
	// from the board or a shell arrives, so the session reads it as its user
	// speaking.
	//
	// This is for trusted, compiled-in extensions only: an extension can
	// speak as the operator. Nothing asks the operator first -- there is no
	// per-press token -- so every extension compiled into the build can use
	// it, and building one in is the trust decision.
	//
	// The message is still queued under this extension's own sender, with
	// its own rate and dedupe budget, and Subject supersedes only this
	// extension's earlier operator-voiced messages: never one the operator
	// typed, and never this extension's fenced ones.
	AsOperator bool
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
