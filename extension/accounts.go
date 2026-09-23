package extension

import (
	"context"
	"time"
)

// AccountPoolProvider is an extension that shares subscriptions between
// operators. The core launches a session on the operator's own login, or on
// an account they name; routing a session onto whichever pooled account has
// headroom needs to know who is asking, which accounts are pooled, who owns
// each, and how much of each is used, and all four belong to the estate
// running the board rather than to the board. A build with no provider
// reports smart routing as unavailable.
//
// The host asks for the pool after configuring the extension, and only
// while it is enabled. A process that also serves the extension's MCP tools
// may configure it a second time before asking, so Configure must be safe to
// repeat.
//
// AccountPool must not return nil while the extension is enabled; one that
// has nothing to share reports itself disabled instead.
type AccountPoolProvider interface {
	AccountPool() AccountPool
}

// AccountPool is one estate's account sharing. Account names are the ones a
// tool's account_secret is built from; the core upper-cases them before
// asking and after answering.
//
// Methods may be called concurrently and must be safe for concurrent use:
// the board's usage monitor reads quota while launches are routed.
type AccountPool interface {
	// Borrower names the operator launching sessions from this machine, as
	// an account name. An error means nobody can be charged, and a routed
	// launch is refused rather than made anonymously.
	Borrower(ctx context.Context) (string, error)
	// Owns reports whether account is borrower's own, so the router spends
	// an operator's own accounts before anyone else's and telemetry can
	// tell a borrowed launch from an owned one. It takes no context and is
	// asked once per candidate while a launch waits, so it must be cheap
	// and must not block.
	Owns(account, borrower string) bool
	// Members lists the accounts a tool's sessions may be routed onto.
	Members(ctx context.Context, tool AccountTool) ([]string, error)
	// Usage reads one account's quota. An account with no usable reading
	// returns an error, and the router passes over it. The core caches
	// each reading for a minute, so the pool need not cache at all.
	Usage(ctx context.Context, tool AccountTool, account string) (AccountUsage, error)
}

// AccountTool is the CLI an account is being chosen for.
type AccountTool struct {
	// Env is the variable the CLI reads its token from.
	Env string
	// Secret is the tool's account_secret, with {account} in it.
	Secret string
	// Listed runs the tool's accounts_command and returns the account
	// names it prints: every account the operator could name, which a pool
	// may narrow.
	Listed func() ([]string, error)
}

// AccountUsage is one account's quota as the pool last observed it.
type AccountUsage struct {
	// ObservedAt is when the reading was taken at its source. Zero means
	// the reading is as fresh as the call that returned it. A reading
	// observed in the future, or more than two minutes before it is used,
	// makes the account ineligible: a pool that caches upstream for longer
	// than that has every account skipped.
	ObservedAt time.Time
	// Windows are the account's rate-limit windows by name. Routing needs
	// "five_hour" and "seven_day"; any other window also counts against
	// the account.
	Windows map[string]UsageWindow
}

// UsageWindow is one rate-limit window.
type UsageWindow struct {
	// Utilization is the percentage used, 0 to 100. Nil means unknown,
	// which makes the account ineligible.
	Utilization *float64
	// ResetsAt is when the window resets. Zero is allowed only for an
	// unused window.
	ResetsAt time.Time
}
