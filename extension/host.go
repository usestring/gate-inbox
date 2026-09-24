package extension

import "context"

// Host is what the board lends an extension to act with: where it keeps its
// files and the sessions it runs. It is a set of narrow services over
// values, never the board's model, store or tmux driver, so an extension
// cannot hold a handle the host later needs to change the shape of.
//
// A Host acts as one session -- the one SessionContext names -- with that
// session's permissions: a session cannot kill or archive itself through it
// any more than through its own tools.
type Host interface {
	// ConfigDir is the operator's config directory, the one config.toml is
	// read from.
	ConfigDir() string
	// Sessions reads and acts on the board's agent sessions.
	Sessions() SessionService
}

// SessionService is the board's agent sessions, as the calling session's
// tools see them. Every value it returns is a copy: changing one changes
// nothing on the board, and a later read is the only way to see a later
// state.
type SessionService interface {
	// Get is one session, without reading its pane.
	Get(ctx context.Context, id string) (SessionInfo, error)
	// List is the sessions filter keeps, in the order the board lists
	// them.
	List(ctx context.Context, filter SessionFilter) (SessionList, error)
	// Spawn launches a new agent session, filed under the calling session
	// unless the request says otherwise.
	Spawn(ctx context.Context, req SpawnRequest) (SessionInfo, error)
	// Send queues text for a session, delivered the way send_session
	// delivers it: at the session's next prompt, not into a busy turn.
	Send(ctx context.Context, id, text string) error
	// Kill ends a session's pane and leaves its row dead.
	Kill(ctx context.Context, id string) error
	// Archive ends a session if it is running and files it out of the
	// active list.
	Archive(ctx context.Context, id string) error
	// Read is what a session is showing. With since empty it is the whole
	// visible pane; with a cursor from an earlier Read it is only the
	// conversation added after it, where the session's transcript can be
	// read.
	Read(ctx context.Context, id, since string) (Screen, error)
}

// SessionInfo is one agent session as it stood when it was read.
type SessionInfo struct {
	ID    string
	Name  string
	Tool  string
	Model string
	// Group is the group path holding the session; empty is the root.
	Group string
	// Directory is the session's current working directory, or its launch
	// directory when it is not running.
	Directory string
	// Status is the board's status: starting, working, waiting, finished,
	// idle, errored or dead.
	Status   string
	Running  bool
	Archived bool
	// ParentID is the session this one is drawn under; SpawnedBy is the one
	// that spawned it and answers its questions. They differ for a spawn
	// made by a session that is itself a child.
	ParentID  string
	SpawnedBy string
}

// SessionFilter narrows List. The zero value is every unarchived session,
// up to the host's default limit.
type SessionFilter struct {
	// ParentID keeps only the sessions drawn under that one, in the order
	// they were created rather than the board's.
	ParentID string
	// Status keeps only sessions in these states; empty keeps every state.
	Status []string
	// IncludeArchived reads archived sessions too, which is slower.
	IncludeArchived bool
	// Limit caps the rows returned; zero takes the host's default, and
	// the host refuses more than it will return in one page.
	Limit int
	// After is a list's Cursor: only the sessions after that page are
	// returned. A cursor holds a place in the order rather than a count,
	// so a session archived or started between two pages neither skips nor
	// repeats a row. It is opaque, and good only for a List with the same
	// filter.
	//
	// With ParentID set, the order is creation order, which nothing
	// changes, so paging reads every session exactly once. Without it the
	// order is the board's, which a reorder, a group move or a replacement
	// taking its predecessor's seat changes: a session moved across the
	// cursor between two pages is skipped or read twice.
	After string
}

// SessionList is what List found. Matched counts before After and Limit,
// so a caller can tell a short board from a truncated one.
type SessionList struct {
	Sessions []SessionInfo
	Matched  int
	// Truncated is whether matching sessions follow this page.
	Truncated bool
	// Cursor is passed as After to read the page after this one, and
	// empty when this page is the last.
	Cursor string
}

// SpawnRequest is a session to launch. Tool is required; everything else
// takes the board's defaults.
type SpawnRequest struct {
	Tool   string
	Name   string
	Prompt string
	// Directory is where it starts; empty inherits the group's directory.
	Directory string
	Model     string
	// Sibling files the new session beside the caller rather than under
	// it, which is the only way to place it in another Group.
	Sibling bool
	Group   string
}

// Screen is one Read of a session.
type Screen struct {
	Session SessionInfo
	// Output is the whole pane when Mode is "pane", and only what was added
	// after the since cursor when Mode is "delta".
	Output string
	Mode   string
	// Cursor is passed back as since to read only what comes after this
	// point. Empty when the session's conversation cannot be read.
	Cursor string
	// Question is the dialog the session is holding, if any, as prose.
	Question string
	// Result is the last thing the session said, bounded.
	Result string
}
