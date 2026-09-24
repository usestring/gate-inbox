package extension

import (
	"context"
	"time"
)

// SpawnPolicy is implemented by an extension that has a say in which new
// agent sessions start: a budget on how far one session's spawns may fan
// out, a rule about which CLIs a group may run.
//
// AllowSpawn is asked before anything is launched, so a refusal costs
// nothing to undo. Its error is the refusal, and it goes back to whoever
// asked -- the agent through the tool it called, the operator in the
// board's error bar -- so it should say what is allowed instead. A panic
// refuses too: a policy that cannot answer does not let the spawn through.
//
// Spawned is told about every spawn that did launch, once its row is
// stored and its pane exists, so a policy counting spawns counts only the
// ones that happened. It is a notice rather than a question: nothing it
// does can take the session back.
//
// Both are asked in whichever process launches the session: the board for
// the operator's own, a session's CLI or MCP server for an agent's. An
// extension that keeps count across them keeps it in its DataDir, or counts
// what Spawn.Sessions reads.
// Terminals and relaunches of an existing row are not spawns, and neither
// is a migration, which MigrationObserver hears about instead.
//
// A policy fails closed. When its extension's config section is refused,
// the extension is disabled, but unlike its other hooks, which are simply
// skipped, the policy is not: every spawn it would have been asked about --
// from the CLI, an MCP tool, the board's forms, or another extension's
// launch -- is refused, with an error naming the extension and what was
// wrong with its section:
//
//	spawn refused: extension "ext1" is disabled: [extensions.ext1]: unknown key(s): bogus
//
// Neither AllowSpawn nor Spawned is called while it is disabled.
type SpawnPolicy interface {
	AllowSpawn(ctx context.Context, spawn Spawn) error
	Spawned(ctx context.Context, spawn Spawn)
}

// Spawn is one new agent session.
type Spawn struct {
	// Session is the row as it will be filed: its ID is final, and so are
	// its parent and group. Status and Running describe a session that has
	// not started yet.
	Session SessionInfo
	// By is who asked for it.
	By SpawnSource
	// Sessions reads the board from whichever process is launching, with
	// the operator's reach: a cap on how many live children one session may
	// have counts them here before the spawn rather than after. Asked in
	// AllowSpawn it reads the board as it stood before this spawn, which is
	// not on it yet; asked in Spawned it is. It opens the board's state for
	// each call, so a policy asks it only when it has something to count.
	Sessions SessionReader
}

// SessionReader reads the board's agent sessions without acting on any of
// them. Every value it returns is a copy.
type SessionReader interface {
	// Get is one agent session, without reading its pane.
	Get(ctx context.Context, id string) (SessionInfo, error)
	// List is the agent sessions filter keeps, in the order the board
	// lists them.
	List(ctx context.Context, filter SessionFilter) (SessionList, error)
}

// SpawnSource is who asked for a spawn.
type SpawnSource string

const (
	// SpawnBySession is an agent spawning through its tools or CLI;
	// Session.SpawnedBy names it.
	SpawnBySession SpawnSource = "session"
	// SpawnByOperator is the operator, from the board.
	SpawnByOperator SpawnSource = "operator"
	// SpawnByExtension is an extension, through BoardHost.Launch.
	SpawnByExtension SpawnSource = "extension"
)

// LaunchContributor is implemented by an extension that puts variables in
// the environment an agent session is launched with: where a hook the
// session runs finds the extension's state, which task the session is
// working on.
//
// LaunchEnv is asked at every launch of an agent's pane -- a spawn, a
// relaunch of a dead row, a migration -- and at a dry run that only shows
// the launch, so it must not write anything. Its variables override what
// the session would inherit from the process launching it; an empty value
// withholds an inherited one. It cannot set the variables the host sets
// itself (the session's id, the executable, the config home, a tool's
// account token or status file), nor tmux's, and two extensions setting
// one variable are refused rather than one silently winning. An error, or
// a panic, refuses the launch.
type LaunchContributor interface {
	LaunchEnv(ctx context.Context, launch Launch) (map[string]string, error)
}

// Launch is one launch of an agent session's pane.
type Launch struct {
	// Session is the row the pane is launched for.
	Session SessionInfo
	Reason  LaunchReason
	// From is the session a migration moves the conversation from, or the
	// one a replacement stands in for; empty for every other reason.
	From string
	// Form is what the operator chose in the fields this extension added to
	// the new-session form, keyed by FormField.Key. It is set only for a
	// spawn from that form; every other launch, a CLI or tool spawn among
	// them, carries none.
	Form map[string]string
}

// SessionFormExtender is implemented by an extension that adds fields of its
// own to the board's new-session form: a toggle that marks a session for
// handling the extension gives it, a choice among a few ways of running it.
//
// SessionFormFields is asked each time the form opens. Its fields are drawn
// after the form's own, in the order given, and are moved through and changed
// like them. What the operator chose reaches the extension's LaunchContributor
// in Launch.Form when the session is launched. A field that is not well formed
// -- no key, a key with a "/", a key given twice, a choice with no options --
// is left off the form, and a panic leaves all of the extension's off.
type SessionFormExtender interface {
	SessionFormFields() []FormField
}

// FormSpawnObserver is implemented by an extension with fields on the
// new-session form that acts on the session the operator spawned from it as
// soon as it exists: opening a view of its own on it, say, when its toggle
// was on.
//
// FormSpawned is called on the board, on its event loop, once the spawn has
// launched and before the board puts the operator in the new session's pane.
// It is called once for each extension whose fields were on the form, with
// the new session and what those fields were set to, keyed by the
// extension's own FormField.Key as in Launch.Form. It is never called for
// any other spawn -- quick and instant spawns, spawns from the CLI or an
// agent's tools, BoardHost.Launch -- nor for a relaunch or a migration.
//
// If the extension opens a view of its own during the call, that view takes
// precedence: the board leaves the cursor on the new row and does not focus
// its pane. Otherwise the board focuses the pane as it does for every spawn
// from the form. The call holds up the board, so it should be quick. It is a
// notice rather than a question: a panic is logged, and the session stays.
type FormSpawnObserver interface {
	FormSpawned(ctx context.Context, sess SessionInfo, form map[string]string)
}

// FormField is one field an extension adds to the new-session form.
type FormField struct {
	// Key names the field's value in Launch.Form. It is the extension's own:
	// two extensions may use the same key.
	Key string
	// Label is what the form shows beside the value; empty shows the key.
	Label string
	Kind  FormFieldKind
	// Options are a choice's values, in the order the form cycles through
	// them. A toggle's are always FormOff and FormOn.
	Options []string
	// Default is the value the form opens on: FormOn or FormOff for a
	// toggle, one of Options for a choice. Anything else is off, or the
	// first option.
	Default string
}

// FormFieldKind is how a form field is changed.
type FormFieldKind string

const (
	// FormToggle is on or off.
	FormToggle FormFieldKind = "toggle"
	// FormChoice is one of a few options.
	FormChoice FormFieldKind = "choice"
)

// A toggle's two values.
const (
	FormOn  = "on"
	FormOff = "off"
)

// LaunchReason is why a pane is being launched.
type LaunchReason string

const (
	// LaunchSpawn is a new session.
	LaunchSpawn LaunchReason = "spawn"
	// LaunchRelaunch is an existing row brought back on its own id: a
	// revive, an unpark, a restart.
	LaunchRelaunch LaunchReason = "relaunch"
	// LaunchMigrate is a new session carrying on another's conversation.
	LaunchMigrate LaunchReason = "migrate"
	// LaunchReplace is a new session a board extension starts in another's
	// place, through BoardHost.Replace.
	LaunchReplace LaunchReason = "replace"
)

// SpawnShaper is implemented by an extension that shapes what an agent's
// own spawns and migrations start with: shared instructions, such as house
// rules or a reporting convention, added to every session it fans out to
// and to the one its conversation moves on to.
//
// ShapeSpawn is asked before SpawnPolicy.AllowSpawn, so a policy is asked
// about the session as shaped, and nothing has been launched or written:
// it must not write anything either, and an error, or a panic, refuses the
// launch. It is asked with LaunchSpawn for a session a session spawns --
// through its tools or its CLI, where Session.SpawnedBy names the caller --
// and with LaunchMigrate for every migration, where From names the source.
// The operator's own spawns and BoardHost.Launch write their prompts
// themselves and are not asked, and neither is a relaunch, which resumes a
// conversation rather than starting one.
type SpawnShaper interface {
	ShapeSpawn(ctx context.Context, launch Launch) (SpawnShape, error)
}

// SpawnShape is how one SpawnShaper shapes a launch. The zero value leaves
// it as it was asked for.
type SpawnShape struct {
	// PromptPrefix goes ahead of the prompt the session is launched with,
	// followed by a blank line. Two shapers' prefixes go in registration
	// order.
	PromptPrefix string
	// PromptSuffix goes after the prompt the session is launched with,
	// behind a blank line: a block the session reads once it has the task,
	// such as how to report back. Two shapers' suffixes go in registration
	// order.
	PromptSuffix string
	// KeepUnderSpawner files a spawn its caller asked to detach, with nest
	// false, under the caller anyway, where a nested spawn would have gone:
	// its questions and rests still reach the session that asked for the
	// work. It takes the caller's group, whatever group was asked for. It
	// means nothing for a migration, which always goes beside its source.
	KeepUnderSpawner bool
}

// MigrationObserver is implemented by an extension that keeps state keyed by
// session and has to follow a conversation when it moves to a new session.
//
// Migrated is called once the new session's row is stored and its pane
// launched, in the process that migrated it, before the migration is
// reported done. An error, or a panic, undoes the migration: the new pane
// is ended and its row deleted, and the error is what the migration
// reports. The source session is left as it was either way.
type MigrationObserver interface {
	Migrated(ctx context.Context, migration Migration) error
}

// Migration is one conversation moving from one session to a new one.
type Migration struct {
	From SessionInfo
	To   SessionInfo
}

// OperatorSendHandler is implemented by an extension that wants to know, in
// the sending process and before the sender is answered, that the operator
// sent a session a line from a shell with `send --as-human`: an extension
// that put a question to the operator can record the line as the answer
// there and then, with no board running.
//
// OperatorSent is called once the message is queued. It observes: the
// message stays queued and is delivered as usual whatever it returns, and
// a running board still reports it through BoardHost.OnOperator as
// OperatorCLI once it is typed in. An extension that listens on both hears
// the one line twice and must count it once: dedupe on MessageID, which
// OperatorInput.MessageID carries for the same message.
//
// A non-empty result says what the extension made of the line, in a word or
// a short phrase ("answered"): the sender prints it and includes it in its
// JSON output, under the extension's ID. An empty result says nothing. An
// error, or a panic, is logged and dropped: the message is already queued,
// and the send still succeeds. Messages sent by agents or by extensions,
// through send_session or BoardHost.Send, are never reported here.
type OperatorSendHandler interface {
	OperatorSent(ctx context.Context, send OperatorSend) (result string, err error)
}

// OperatorSend is one line the operator sent a session from a shell.
type OperatorSend struct {
	// Session is the recipient.
	Session SessionInfo
	// MessageID is the queued message's id. The board's later OperatorCLI
	// report of the same line carries it as OperatorInput.MessageID.
	MessageID int64
	Text      string
	Subject   string
	Interrupt bool
	At        time.Time
}

// OperatorSendResult is what one extension made of an operator's send.
type OperatorSendResult struct {
	Extension string `json:"extension"`
	Result    string `json:"result"`
}

// LaunchRequest is a session an extension starts from the board, through
// BoardHost.Launch. Tool is required; everything else takes the board's
// defaults.
// LaunchPlan is what a launch would start, read without starting it: the
// id the new session would have, and its pane's command line and
// environment exactly as the pane would be given them. Env is the whole
// environment the launch sets, inherited values included, so it can hold
// secrets such as an account's token: it is for comparing and inspecting,
// not for printing whole.
type LaunchPlan struct {
	SessionID string
	Command   string
	Env       map[string]string
}

type LaunchRequest struct {
	Tool   string
	Name   string
	Prompt string
	// Directory is where it starts; empty is the parent's directory, or the
	// group's when there is no parent.
	Directory string
	Model     string
	// ParentID files the session under that one. It is filed as a leaf, so
	// the parent may itself be somebody's child; nothing is ever filed
	// under it in turn.
	ParentID string
	// Group is where a session with no parent goes; empty is the root.
	Group string
	// Role tags the session as one the extension launched for a purpose of
	// its own. It is recorded as "<extension id>/<role>", and SessionInfo
	// reports it back that way, so one extension's roles can never be
	// mistaken for another's.
	Role string
	// Args are appended to the tool's command line after the prompt, each
	// quoted as one word.
	Args []string
}

// KillObserver is implemented by an extension that keeps state for a
// session's life and has to close it when the session is killed: a record
// of what the session was working on, a lease it held.
//
// Killed is called once the session's pane has ended and its row reads
// dead, in the process that killed it -- a session's CLI, its MCP server,
// or the board -- so it is heard whether or not a board is running. A kill
// that fails is not reported. It is a notice rather than a question:
// nothing it does brings the session back, and a panic in it is logged
// rather than failing the kill. Observers are told in registration order.
//
// Only a kill is reported. Parking, archiving and a restart also end a
// pane, but each means to bring the session back or file it away, and a
// board extension sees those through its status events.
type KillObserver interface {
	Killed(ctx context.Context, kill KillContext)
}

// KillContext is one session that was killed.
type KillContext struct {
	// Session is the row as it stands after the kill: Status is dead and
	// Running is false.
	Session SessionInfo
	// Via is the path the kill came through.
	Via KillSource
	// By is the session that asked for the kill, or empty when no session
	// did: a person at a shell, or the board.
	By string
}

// KillSource is the path a kill came through.
type KillSource string

const (
	// KillByCLI is the kill command.
	KillByCLI KillSource = "cli"
	// KillByMCP is the kill_session tool.
	KillByMCP KillSource = "mcp"
	// KillByBoard is the board's own kill, which an extension running on
	// the board asks for through BoardHost.Kill.
	KillByBoard KillSource = "board"
	// KillByExtension is an extension acting as a session, through its
	// Host's SessionService.Kill.
	KillByExtension KillSource = "extension"
)
