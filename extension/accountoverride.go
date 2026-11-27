package extension

import "context"

// LaunchAccountOverrider is implemented by an AccountChooser that has the
// last word on every launch's account, including launches that name one
// (spawns, forks, revives, restarts, migrations). The host asks once the
// account is settled and before its token is read; the returned account is
// recorded and its token read.
//
// It is never asked for a preview. An override that errors or panics is
// logged and the settled account stands.
type LaunchAccountOverrider interface {
	OverrideLaunchAccount(ctx context.Context, req LaunchAccountRequest) (LaunchAccount, error)
}

// LaunchAccountRequest is one launch whose account is settled.
type LaunchAccountRequest struct {
	// SessionID is the session being launched.
	SessionID string
	// ToolName is the CLI's name in the config, and Tool its account
	// settings.
	ToolName string
	Tool     AccountTool
	Reason   LaunchReason
	// From is the session a fork copies or a migration moves the
	// conversation from; empty otherwise.
	From string
	// Account is the account settled so far, "" for the CLI's own login.
	Account string
}

// LaunchAccount is an overrider's answer. The zero value keeps the settled
// account.
type LaunchAccount struct {
	// Override replaces the settled account with Account, "" being the
	// CLI's own login.
	Override bool
	Account  string
	// Warning is one line passed to the launch's warning.
	Warning string
}
