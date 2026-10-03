package extension

import "context"

// AccountChooserProvider is an extension that decides which named account a
// launch runs on when nobody named one. The core itself only ever launches
// on the CLI's own login or on an account somebody named; any policy for
// choosing among accounts belongs to the build's extension. A build with no
// provider launches on the CLI's own login.
//
// The host asks for the chooser after configuring the extension, and only
// while it is enabled. A process that also serves the extension's MCP tools
// may configure it a second time before asking, so Configure must be safe to
// repeat. AccountChooser must not return nil while the extension is
// enabled; one with nothing to choose reports itself disabled instead.
type AccountChooserProvider interface {
	AccountChooser() AccountChooser
}

// AccountChooser chooses a launch's account. Methods may be called
// concurrently, from the board, a CLI command and every session's MCP
// server, and must be safe for concurrent use.
type AccountChooser interface {
	// ChooseAccount is the named account the launch runs on, as a tool's
	// account_secret spells it, or "" for the CLI's own login. An error
	// refuses the launch. The core asks it for a launch that names no
	// account while the operator has the extension choosing accounts, and
	// for every migration, which never takes an account from its caller.
	ChooseAccount(ctx context.Context, req AccountRequest) (string, error)
}

// AccountRequest is one launch asking for an account.
type AccountRequest struct {
	// SessionID is the session being launched; empty for a preview that
	// has no session yet.
	SessionID string
	// ToolName is the CLI's name in the config, and Tool its account
	// settings.
	ToolName string
	Tool     AccountTool
	Reason   LaunchReason
	// From is the session a migration moves the conversation from.
	From string
	// Preview asks what the launch would get without launching anything:
	// a chooser that takes turns among accounts takes none for it.
	Preview bool
}

// AccountTool is the CLI an account is being chosen for.
type AccountTool struct {
	// Env is the variable the CLI reads its token from.
	Env string
	// Secret is the tool's account_secret, with {account} in it.
	Secret string
	// Listed runs the tool's accounts_command and returns the account
	// names it prints: every account the operator could name.
	Listed func() ([]string, error)
}

// ToolChooser is implemented by an AccountChooser that can also pick which
// CLI a new session runs on. The board offers its "auto" choice for new
// sessions only when the build's chooser implements it.
type ToolChooser interface {
	// ChooseTool is one of req.Candidates' names. An error leaves the
	// operator to choose.
	ChooseTool(ctx context.Context, req ToolRequest) (string, error)
}

// ToolQuotaReporter is implemented by a ToolChooser that can also say how
// much quota each CLI has left. The board lists it under the CLIs in the
// new-session box.
type ToolQuotaReporter interface {
	// ToolQuotaSummaries is one line for each named CLI it has a reading
	// for; a CLI it cannot read is left out. It is called off the event
	// loop, but the box waits on it, so it should answer from a cache.
	ToolQuotaSummaries(ctx context.Context, names []string) map[string]string
}

// ToolRequest is a new session whose CLI the operator left to the build.
type ToolRequest struct {
	// Candidates are the enabled agent CLIs, in the board's order.
	Candidates []ToolCandidate
	// Active is how many sessions each CLI already has in flight.
	Active map[string]int
	// ChoosingAccounts says the operator has the extension choosing
	// accounts, so a CLI with account settings would launch on one the
	// chooser picks rather than on its own login.
	ChoosingAccounts bool
}

// ToolCandidate is one CLI a new session could run on.
type ToolCandidate struct {
	Name string
	// Account is the CLI's account settings, or nil for a CLI that cannot
	// be launched on a named account.
	Account *AccountTool
}
