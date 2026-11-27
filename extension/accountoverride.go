package extension

import "context"

// LaunchAccountOverrider is implemented by an AccountChooser that must have
// the last word on the account every launch runs on, including the launches
// it is not asked to choose for: a spawn or fork that names its account (a
// fork copies its source's), a revive or restart that keeps the one its
// conversation is on, and a migration. The host asks it once the account is
// settled -- named by the launch, picked by the chooser, or the CLI's own
// login -- and before the account's token is read, so an override takes the
// launch's place in full: the row records the account it returns and the
// token read is that account's.
//
// It is never asked for a preview. An override that errors or panics is
// logged and ignored, and the settled account stands.
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
	// Warning, when set, is one line on why the launch runs where it does,
	// passed to the launch's warning like a chooser's fallback.
	Warning string
}
