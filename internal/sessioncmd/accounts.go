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
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return "", err
	}
	if tool != "" {
		spec, ok := cfg.Tools[tool]
		if !ok {
			return "", fmt.Errorf("unknown tool %q (configured: %s)", tool, strings.Join(toolNames(cfg), ", "))
		}
		return accountsFor(tool, spec), nil
	}
	var b strings.Builder
	for _, name := range toolNames(cfg) {
		spec := cfg.Tools[name]
		if spec.AccountEnv == "" {
			continue
		}
		b.WriteString(accountsFor(name, spec))
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "no configured tool can be launched on a named account", nil
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// accountsFor is one tool's section. A tool that cannot answer says so in
// place rather than being dropped, for the reason modelsFor gives.
func accountsFor(name string, spec config.Tool) string {
	if spec.AccountEnv == "" {
		return name + ": cannot be launched on a chosen account (no account_env)\n"
	}
	names, err := accounts.List(spec)
	if err != nil {
		return fmt.Sprintf("%s: listing accounts failed: %v\n", name, err)
	}
	if len(names) == 0 {
		return fmt.Sprintf("%s: no accounts found by %q\n", name, spec.AccountsCommand)
	}
	return fmt.Sprintf("%s (%d, from %q):\n%s\n", name, len(names), spec.AccountsCommand, strings.Join(names, "\n"))
}

func (r *runtime) accountOr(named, toolName string, tool config.Tool, sessionID string, reason extension.LaunchReason) (string, error) {
	return accounts.Select(r.store, tool, named, accounts.Request{SessionID: sessionID, ToolName: toolName, Reason: reason})
}

// SwitchAccount migrates Claude contexts over 200k to avoid replaying them on a new account.
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
	return s.switchAccount(runtime, target, account, func(account string) (Session, error) {
		return s.Migrate(sessionID, targetID, MigrateOptions{Tool: target.Tool, accountOverride: &account})
	})
}

// switchAccount re-points target at account and resumes it there. A context
// too large to resume on another account goes to large instead.
func (s *Sessions) switchAccount(runtime *runtime, target store.Session, account string, large func(string) (Session, error)) (Session, error) {
	tool, known := runtime.cfg.Tools[target.Tool]
	if !known {
		return Session{}, fmt.Errorf("tool %s is no longer configured", target.Tool)
	}
	account, err := launch.AccountForSwitch(tool, target.TmuxPaneID != "", account)
	if err != nil {
		return Session{}, err
	}
	if account == target.Account {
		return runtime.sessionInfo(target, runtime.driver.Exists(target.ID), false), nil
	}
	if _, over := migrate.AccountSwitchTranscript(s.roots, tool, target); over {
		return large(account)
	}
	if err := runtime.store.SetAccount(target.ID, account); err != nil {
		return Session{}, err
	}
	target.Account = account
	if !runtime.driver.Exists(target.ID) {
		return runtime.sessionInfo(target, false, false), nil
	}
	if err := s.endSession(runtime, target, store.EndKilled); err != nil {
		return Session{}, err
	}
	relaunched, err := s.relaunch(runtime, target, "")
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
	return s.switchAccount(runtime, target, account, func(string) (Session, error) {
		return Session{}, fmt.Errorf("%s's context is too large to resume on another account; switch_account moves it to a new conversation instead", target.Name)
	})
}
