package extension

import (
	"context"
	"errors"
	"time"
)

// ToolDriverProvider is an extension that teaches the board agent CLIs the
// core has no code for. A [tools.<name>] block is enough to launch any CLI;
// what a block cannot say is how to hand that CLI the board's MCP server,
// how to read back the conversation id it minted, where it keeps a
// conversation on disk, and how another agent can read one. A driver is that
// code, supplied by the build rather than copied into it.
//
// The host asks for drivers after configuring the extension, and only while
// it is enabled. Like an AccountPoolProvider, it may be configured a second
// time by a process that also serves its MCP tools.
type ToolDriverProvider interface {
	ToolDrivers() []ToolDriver
}

// ToolDriver is one agent CLI's integration, named by its style.
//
// A tool block selects a driver with mcp = "<style>" for MCP registration
// and session_store = "<style>" for the rest; a block keyed by the style
// ([tools.<style>]) selects it for MCP registration without saying so. A
// style no built-in and no enabled driver answers to is refused, never
// treated as "none".
//
// A driver that cannot do one of the four returns errors.ErrUnsupported from
// it. Embed UnsupportedToolDriver to start from a driver that supports
// nothing.
//
// Methods may be called concurrently, from the board and from any session's
// CLI commands at once.
type ToolDriver interface {
	// Style is the name tool blocks select this driver by. It follows the
	// extension ID rules, and may not be one of the core's built-in styles.
	Style() string
	// RegisterMCP makes the board's MCP server visible to a session about
	// to launch. It returns the command to run and any environment to add.
	// With req.DryRun it composes the same answer and writes nothing, runs
	// nothing and registers nothing.
	RegisterMCP(ctx context.Context, req MCPRequest) (MCPLaunch, error)
	// CaptureSession returns the conversation id the CLI minted for a
	// session launched in req.Directory at or after req.LaunchedAt, skipping
	// every id req.Claimed reports as another session's. An empty id with no
	// error means no confident match yet; the board asks again on its next
	// poll. ValidSessionID, SamePath and EarliestSession are the checks the
	// built-in stores capture with, so a driver need not write its own.
	CaptureSession(ctx context.Context, req CaptureRequest) (string, error)
	// SessionFile is the file on disk holding conversation id, for a
	// fork_command that loads a conversation from a file ({session_file}).
	SessionFile(ctx context.Context, id string) (string, error)
	// MigrateTranscript tells an agent taking over a conversation where to
	// read it.
	MigrateTranscript(ctx context.Context, req TranscriptRequest) (Transcript, error)
}

// UnsupportedToolDriver answers errors.ErrUnsupported to every capability.
// Embed it and override what a CLI supports; the embedding type supplies
// Style.
type UnsupportedToolDriver struct{}

func (UnsupportedToolDriver) RegisterMCP(context.Context, MCPRequest) (MCPLaunch, error) {
	return MCPLaunch{}, errors.ErrUnsupported
}

func (UnsupportedToolDriver) CaptureSession(context.Context, CaptureRequest) (string, error) {
	return "", errors.ErrUnsupported
}

func (UnsupportedToolDriver) SessionFile(context.Context, string) (string, error) {
	return "", errors.ErrUnsupported
}

func (UnsupportedToolDriver) MigrateTranscript(context.Context, TranscriptRequest) (Transcript, error) {
	return Transcript{}, errors.ErrUnsupported
}

// MCPRequest is a session launch the board's MCP server is to be registered
// into.
type MCPRequest struct {
	// ServerName is the name the MCP server is registered under, which is
	// what names its tools inside the CLI.
	ServerName string
	// Executable is the board's binary; the server is Executable with the
	// single argument "mcp".
	Executable string
	// SessionIDEnv is the variable holding the session's id in its
	// environment. The server needs it forwarded, in whatever reference
	// syntax the CLI expands in a server's env block.
	SessionIDEnv string
	// SessionID is the id of the session being launched.
	SessionID string
	// HooksDir is the board's directory for generated launch files, shared
	// by every session. A per-session file needs the session id in its name.
	HooksDir string
	// Command is the launch command so far.
	Command string
	// Env is the environment the session will get. It is a copy: add to it
	// through MCPLaunch.Env.
	Env map[string]string
	// Model is the model the session was asked for, empty for the CLI's
	// default.
	Model string
	// DryRun composes the launch for a person to read: nothing is written,
	// run or registered.
	DryRun bool
}

// MCPLaunch is a launch with the MCP server registered.
type MCPLaunch struct {
	// Command replaces the launch command. Empty keeps it as it was.
	Command string
	// Env is added to the session's environment. A key the host sets for
	// its own use cannot be overridden.
	Env map[string]string
}

// CaptureRequest is one session whose conversation id is still unknown.
type CaptureRequest struct {
	// Directory is the session's working directory.
	Directory string
	// LaunchedAt is when the session's CLI was started.
	LaunchedAt time.Time
	// Claimed reports whether an id already belongs to another session.
	Claimed func(id string) bool
}

// TranscriptRequest is a conversation to hand over.
type TranscriptRequest struct {
	// ID is the conversation id the CLI minted.
	ID string
	// Directory is the session's working directory.
	Directory string
}

// A driver's MigrateTranscript answers with a Transcript (see
// conversation.go): exactly one of Path and Command set, and Format telling
// the agent taking over how it is laid out.
