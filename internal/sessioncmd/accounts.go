// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/launchpace"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/store"
)

// Accounts answers what create_session's account argument accepts for a
// tool: the named subscriptions whose tokens its accounts_command can see.
//
// It is the same call as Models for the same reason. A caller with no way to
// find the names guesses, and a guessed account is a refused launch: the
// secret does not exist, so the session never starts. Naming one tool answers
// for that tool; naming none lists every tool that can run on an account.
func Accounts(configDir, tool string) (string, error) {
	list, err := ListAccounts(configDir, tool)
	if err != nil {
		return "", err
	}
	return FormatAccounts(list), nil
}

// AccountList is Accounts before it is written out, for a caller that
// wants the names rather than the sentence.
type AccountList struct {
	// Tool is set when one tool was asked for, which then answers even when
	// it cannot be launched on an account.
	Tool  string         `json:"tool,omitempty"`
	Tools []ToolAccounts `json:"tools"`
}

// ToolAccounts is one tool's answer. Problem says why it has no names when
// it could not list them, and is empty when it simply found none.
type ToolAccounts struct {
	Tool            string   `json:"tool"`
	AccountsCommand string   `json:"accounts_command,omitempty"`
	Accounts        []string `json:"accounts"`
	Problem         string   `json:"problem,omitempty"`
}

// ListAccounts is Accounts as data.
func ListAccounts(configDir, tool string) (AccountList, error) {
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return AccountList{}, err
	}
	if tool != "" {
		spec, ok := cfg.Tools[tool]
		if !ok {
			return AccountList{}, fmt.Errorf("unknown tool %q (configured: %s)", tool, strings.Join(toolNames(cfg), ", "))
		}
		return AccountList{Tool: tool, Tools: []ToolAccounts{accountsFor(tool, spec)}}, nil
	}
	list := AccountList{Tools: []ToolAccounts{}}
	for _, name := range toolNames(cfg) {
		spec := cfg.Tools[name]
		if spec.AccountEnv == "" {
			continue
		}
		list.Tools = append(list.Tools, accountsFor(name, spec))
	}
	return list, nil
}

// accountsFor is one tool's section. A tool that cannot answer says so in
// place rather than being dropped, for the reason modelsFor gives.
func accountsFor(name string, spec config.Tool) ToolAccounts {
	answer := ToolAccounts{Tool: name, AccountsCommand: spec.AccountsCommand, Accounts: []string{}}
	if spec.AccountEnv == "" {
		answer.Problem = "cannot be launched on a chosen account (no account_env)"
		return answer
	}
	names, err := accounts.List(spec)
	if err != nil {
		answer.Problem = fmt.Sprintf("listing accounts failed: %v", err)
		return answer
	}
	if names != nil {
		answer.Accounts = names
	}
	return answer
}

// FormatAccounts writes list the way list_accounts answers.
func FormatAccounts(list AccountList) string {
	var b strings.Builder
	for _, tool := range list.Tools {
		switch {
		case tool.Problem != "":
			fmt.Fprintf(&b, "%s: %s\n", tool.Tool, tool.Problem)
		case len(tool.Accounts) == 0:
			fmt.Fprintf(&b, "%s: no accounts found by %q\n", tool.Tool, tool.AccountsCommand)
		default:
			fmt.Fprintf(&b, "%s (%d, from %q):\n%s\n", tool.Tool, len(tool.Accounts), tool.AccountsCommand, strings.Join(tool.Accounts, "\n"))
		}
		if list.Tool == "" {
			b.WriteString("\n")
		}
	}
	if list.Tool != "" {
		return b.String()
	}
	if b.Len() == 0 {
		return "no configured tool can be launched on a named account"
	}
	return strings.TrimRight(b.String(), "\n")
}

// Accounts is ListAccounts against this layer's config, for the CLI.
func (s *Sessions) Accounts(tool string) (AccountList, error) {
	return ListAccounts(s.configDir, tool)
}

func (r *runtime) accountOr(named, toolName string, tool config.Tool, sessionID string, reason extension.LaunchReason) (string, error) {
	return accounts.Select(r.store, tool, named, accounts.Request{SessionID: sessionID, ToolName: toolName, Reason: reason})
}

// SwitchAccount migrates a Claude context over its tier's account-switch
// cutoff (config.Tool.AccountSwitchCutoff) to a new conversation, to avoid
// replaying it uncached on a new account.
// Otherwise it moves the session onto another named subscription. The row is
// re-pointed first; a running session is then ended and brought back on the
// conversation it holds, since the token is read at launch and nothing else
// can hand a live process a new one. A dead session is only re-pointed and
// comes back on the account at its next revive. Empty moves it back to the
// CLI's own login.
//
// A session cannot switch itself: the relaunch ends the process making the
// call, so the answer would never arrive. It asks the operator, or a
// sibling, instead.
func (s *Sessions) SwitchAccount(sessionID, targetID, account string) (Session, error) {
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Session{}, err
	}
	target, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	if target.ID == sessionID {
		return Session{}, errors.New("a session cannot switch its own account: the restart would end this call; ask the operator or a sibling session")
	}
	return s.switchAccount(runtime, target, account, true, func(account string) (Session, error) {
		return s.Migrate(sessionID, targetID, MigrateOptions{Tool: target.Tool, accountOverride: &account})
	})
}

// switchAccount re-points target at account and resumes it there. A context
// too large to resume on another account goes to large instead. A running
// session takes its relaunch queue turn before the row is re-pointed, so a
// refusal leaves it on the account it runs on; wait says whether to wait
// for that turn.
func (s *Sessions) switchAccount(runtime *runtime, target store.Session, account string, wait bool, large func(string) (Session, error)) (Session, error) {
	tool, known := runtime.cfg.Tools[target.Tool]
	if !known {
		return Session{}, fmt.Errorf("tool %s is no longer configured", target.Tool)
	}
	account, err := launch.AccountForSwitch(tool, target.TmuxPaneID != "", account)
	if err != nil {
		return Session{}, err
	}
	if account == target.Account {
		return runtime.currentInfo(target, false), nil
	}
	tier, err := runtime.store.SizingTier(target)
	if err != nil {
		return Session{}, err
	}
	if _, over := migrate.AccountSwitchTranscript(s.roots, tool, target, tool.AccountSwitchCutoff(tier)); over {
		return large(account)
	}
	running := runtime.driver.Exists(target.ID)
	var slot launchpace.Slot
	if running {
		if slot, err = s.pace(runtime, target.ID, wait); err != nil {
			return Session{}, err
		}
	}
	if err := runtime.store.SetAccount(target.ID, account); err != nil {
		slot.Cancel()
		return Session{}, err
	}
	target.Account = account
	if !running {
		return runtime.sessionInfo(target, false, false), nil
	}
	relaunched, err := s.restart(runtime, target, slot)
	if err != nil {
		return Session{}, err
	}
	target.Status = relaunched.DefaultStatus
	return runtime.sessionInfo(target, true, false), nil
}

// BoardAccount is the account an agent session runs on, "" for its CLI's
// own login, and that CLI's account settings, read with the operator's
// reach.
func (s *Sessions) BoardAccount(targetID string) (string, extension.AccountTool, error) {
	runtime, err := s.open()
	if err != nil {
		return "", extension.AccountTool{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return "", extension.AccountTool{}, err
	}
	tool, known := runtime.cfg.Tools[target.Tool]
	if !known {
		return "", extension.AccountTool{}, fmt.Errorf("tool %s is no longer configured", target.Tool)
	}
	return target.Account, accounts.Tool(tool), nil
}

// BoardSwitchAccount is SwitchAccount on the board's behalf, for a board
// extension. It has no calling session to file a migration under, so a
// context too large to resume on another account is refused rather than
// moved to a new conversation.
// It does not wait for the relaunch queue: while another session is
// relaunching it is refused with launchpace.ErrBusy, and the extension asks
// again on a later pass.
func (s *Sessions) BoardSwitchAccount(targetID, account string) (Session, error) {
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return Session{}, err
	}
	return s.switchAccount(runtime, target, account, false, func(string) (Session, error) {
		return Session{}, fmt.Errorf("%s's context is too large to resume on another account; switch_account moves it to a new conversation instead", target.Name)
	})
}
