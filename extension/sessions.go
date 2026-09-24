package extension

import "context"

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
// extension that keeps count across them keeps it in its DataDir.
// Terminals and relaunches of an existing row are not spawns, and neither
// is a migration, which MigrationObserver hears about instead.
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
}

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
