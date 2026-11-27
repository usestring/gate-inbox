package extension

import (
	"context"
	"time"
)

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
	// Exclude names accounts this launch must not run on: the core asks
	// again with an account in it after the chooser picked one whose
	// secret cannot be read (its version disabled or destroyed, the secret
	// gone, or the operator not allowed to read it). A chooser that answers
	// with an excluded account anyway has the launch fall back to the CLI's
	// own login.
	Exclude []string
}

// AccountUnavailableReporter is implemented by an AccountChooser that wants
// to hear when an account it chose cannot be launched on because its secret
// cannot be read, so it can stop choosing it and forget any record that the
// launch went there. The core calls it before asking again with the account
// in AccountRequest.Exclude. err says why, in one line.
type AccountUnavailableReporter interface {
	AccountUnavailable(ctx context.Context, req AccountRequest, account string, err error)
}

// AccountAdvisor is implemented by an AccountChooser that knows what state
// each account is in: which ones its provider would refuse right now, until
// when, and how much of each usage window is used. list_accounts shows it
// beside each name, so an agent picking an account has something to pick
// with, and a new session that names an account the advisor reports spent
// does not launch there: the core asks the chooser for another account
// instead, and refuses the launch with the account's reset when the chooser
// has none.
//
// It is called while a caller waits, from the board, a CLI command and every
// session's MCP server, so it must answer from what it already knows and be
// safe for concurrent use. An error leaves the accounts shown without state
// and the named account launched on as named.
type AccountAdvisor interface {
	AdviseAccounts(ctx context.Context, req AdviceRequest) (Advice, error)
}

// AdviceRequest asks for the state of some of one CLI's accounts.
type AdviceRequest struct {
	ToolName string
	Tool     AccountTool
	// Accounts are the names asked about, spelled as account_secret takes
	// them.
	Accounts []string
}

// Advice is an AccountAdvisor's answer.
type Advice struct {
	// Accounts is each asked-about account the advisor has a reading for,
	// by name; one it knows nothing about is left out, and is shown and
	// launched on as if there were no advisor. A reading past its window's
	// reset is no reading: leave such an account out rather than report it
	// spent.
	Accounts map[string]AccountState
	// Suggested is the account the advisor would launch a new session on
	// now, "" when it would choose none. It is shown, never launched on by
	// itself: a launch still asks ChooseAccount.
	Suggested string
}

// AccountState is what an advisor knows about one account.
type AccountState struct {
	// Spent says the provider would refuse a session on it now.
	Spent bool
	// Until is when a spent account is expected to take sessions again;
	// zero when unknown.
	Until time.Time
	// Reason says in a few words why it is spent ("at its weekly limit"),
	// or anything else an agent choosing it should know.
	Reason string
	// Windows are its usage windows, in the order to show them.
	Windows []UsageWindow
}

// UsageWindow is one rate-limit window's reading.
type UsageWindow struct {
	// Name is the window as the provider names it ("five_hour").
	Name string
	// Used is the share of the window used, in percent.
	Used float64
	// ResetsAt is when the window resets; zero when unknown.
	ResetsAt time.Time
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

// ToolChoiceExplainer is implemented by a ToolChooser that can say why it
// picks the CLI it picks, or why it picks none. The board shows the reason
// under the CLIs in the new-session box, on the status bar when auto starts
// a session, and beside the error when auto falls back to the box.
type ToolChoiceExplainer interface {
	// ExplainToolChoice answers as ChooseTool does, with the reason in one
	// line; the reason may be empty, and is shown on failure too.
	ExplainToolChoice(ctx context.Context, req ToolRequest) (name, reason string, err error)
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

// limitMoveKey marks a context as a move off an account at its limit.
type limitMoveKey struct{}

// LimitMove marks ctx's BoardHost.SwitchAccount as a move of a session its
// account's limit has already cut off. That session can run nothing where
// it is, so its relaunch goes ahead of routine moves in the relaunch queue
// and may run beside one other; a move made before the limit keeps the
// queue's one-at-a-time pace.
func LimitMove(ctx context.Context) context.Context {
	return context.WithValue(ctx, limitMoveKey{}, true)
}

// IsLimitMove says ctx was marked by LimitMove.
func IsLimitMove(ctx context.Context) bool {
	on, _ := ctx.Value(limitMoveKey{}).(bool)
	return on
}

// LimitEpisode is the board's decision about one session held at its
// account's usage limit. The board records each limit banner it sees, and
// the limit card, its own continuation after a reset, and an extension
// moving sessions between accounts all act on the same decision.
type LimitEpisode struct {
	// Banner is the limit as the pane shows it.
	Banner string
	// Account is the account that hit the limit, "" for the CLI's own login.
	Account string
	// Since is when the board first saw the banner.
	Since time.Time
	// Cut says the limit refused a live turn, so somebody is waiting on the
	// work. A session merely showing a banner its relaunch replayed is not
	// cut: moving or continuing it would spend a turn nobody asked for, and
	// the next message it is sent finds out whether its account has room.
	Cut bool
	// Move says the session is worth moving to another account now, and
	// continuing there once: it was cut, it is running and not muted, and
	// its CLI runs on named accounts. A move relaunches it, and the
	// relaunch's replayed banner is no new cut, so each episode asks for one
	// move.
	Move bool
}

// LimitReader is implemented by a BoardHost that records limit episodes.
type LimitReader interface {
	// LimitEpisodes is every session the board finds held at a limit that
	// is still its own, by id: not archived, not migrated to another
	// session, and still on the account that hit it.
	LimitEpisodes(ctx context.Context) (map[string]LimitEpisode, error)
}
