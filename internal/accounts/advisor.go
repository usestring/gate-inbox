package accounts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// adviseTimeout bounds one question put to the advisor. It answers from
// what it already knows, and a list_accounts or a spawn waits on it, so a
// slow one is cut off and the accounts are shown, and launched on, as if
// there were none.
const adviseTimeout = 5 * time.Second

// now is the clock spent accounts' resets are told against, replaced in
// tests.
var now = time.Now

// Advise is the build's advice on names, a tool's accounts, and false when
// the build has no advisor or it could not answer. Each state is keyed by
// the normalized name.
func Advise(tool config.Tool, toolName string, names []string) (extension.Advice, bool) {
	chooser, err := Chooser()
	if err != nil || chooser == nil {
		return extension.Advice{}, false
	}
	advisor, ok := chooser.(extension.AccountAdvisor)
	if !ok {
		return extension.Advice{}, false
	}
	return advise(advisor, tool, toolName, names)
}

func advise(advisor extension.AccountAdvisor, tool config.Tool, toolName string, names []string) (extension.Advice, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), adviseTimeout)
	defer cancel()
	asked := make([]string, len(names))
	for i, name := range names {
		asked[i] = Normalize(name)
	}
	advice, err := advisor.AdviseAccounts(ctx, extension.AdviceRequest{ToolName: toolName, Tool: Tool(tool), Accounts: asked})
	if err != nil {
		logging.Warn("account advisor did not answer", "tool", toolName, logging.Err(err))
		return extension.Advice{}, false
	}
	states := make(map[string]extension.AccountState, len(advice.Accounts))
	for name, state := range advice.Accounts {
		states[Normalize(name)] = state
	}
	advice.Accounts = states
	advice.Suggested = Normalize(advice.Suggested)
	return advice, true
}

// SpentError refuses a new session on a named account the build's advisor
// reports spent, when the chooser had no other account to launch it on.
type SpentError struct {
	Account string
	State   extension.AccountState
}

func (e *SpentError) Error() string {
	return fmt.Sprintf("account %s is %s, and the build chose no other account to launch on instead; name another account from list_accounts, or omit account", e.Account, DescribeSpent(e.State, now()))
}

// DescribeSpent is a spent account's state in a few words: "spent until
// 2026-03-04 15:00 UTC (in 2h10m): at its weekly limit".
func DescribeSpent(state extension.AccountState, at time.Time) string {
	text := "spent"
	if !state.Until.IsZero() {
		text += " until " + state.Until.UTC().Format("2006-01-02 15:04 UTC")
		if left := state.Until.Sub(at); left > 0 {
			text += " (in " + left.Round(time.Minute).String() + ")"
		}
	}
	if reason := strings.TrimSpace(state.Reason); reason != "" {
		text += ": " + reason
	}
	return text
}

// avoidSpent is the account a new session that names one runs on. A name
// the advisor does not report spent is launched on as named. A spent one is
// a launch that would die on its first turn, so the chooser is asked for
// another account with the named one excluded, and the caller is warned
// which; with no other account to give, the launch is refused with the
// account's reset.
//
// The chooser is asked whatever the launch-account mode, as for a
// migration: the caller asked for a named account, not for the CLI's own
// login, so the CLI's own login is not offered in its place.
func avoidSpent(tool config.Tool, named string, req Request, preview bool) (string, error) {
	chooser, err := Chooser()
	if err != nil || chooser == nil {
		return named, nil
	}
	advisor, ok := chooser.(extension.AccountAdvisor)
	if !ok {
		return named, nil
	}
	advice, ok := advise(advisor, tool, req.ToolName, []string{named})
	if !ok {
		return named, nil
	}
	state, known := advice.Accounts[named]
	if !known || !state.Spent {
		return named, nil
	}
	chosen, err := ask(chooser, tool, Request{SessionID: req.SessionID, ToolName: req.ToolName, Reason: req.Reason, From: req.From}, preview, []string{named})
	if err == nil && chosen == "" {
		err = &SpentError{Account: named, State: state}
	}
	recordRedirect(req, named, chosen, err)
	if err != nil {
		return "", err
	}
	if req.Warn != nil {
		req.Warn(fmt.Sprintf("account %s is %s; launched on %s instead", named, DescribeSpent(state, now()), chosen))
	}
	return chosen, nil
}

// recordRedirect traces a named account found spent at launch, and where
// the launch went instead.
func recordRedirect(req Request, named, chosen string, err error) {
	at := time.Now()
	tracing.Record("account.spent_named", at, at, err,
		tracing.Attr{Key: "session", Value: req.SessionID},
		tracing.Attr{Key: "tool", Value: req.ToolName},
		tracing.Attr{Key: "account.named", Value: named},
		tracing.Attr{Key: "account.name", Value: chosen})
}
