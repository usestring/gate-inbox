package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/store"
)

// accountState is the switch-account card: the session it is about and the
// accounts it may move to, "own login" first.
type accountState struct {
	sess    store.Session
	names   []string
	index   int
	migrate bool
}

// openAccountSwitch opens the card for the session under the cursor. A
// token is read at launch, so moving a session needs a restart or migration.
func (m *Model) openAccountSwitch() {
	entry, ok := m.selectedRow()
	if !ok {
		return
	}
	if !entry.isSession() {
		m.errBar.text = "select a session to switch its account"
		return
	}
	tool, ok := m.cfg.Tools[entry.sess.Tool]
	if !ok {
		m.errBar.text = fmt.Sprintf("tool %s is no longer configured", entry.sess.Tool)
		return
	}
	if tool.AccountEnv == "" {
		m.errBar.text = entry.sess.Tool + " cannot be launched on a chosen account (no account_env)"
		return
	}
	if entry.sess.TmuxPaneID != "" {
		m.errBar.text = entry.sess.Name + " is a pane the manager did not start; take it over first"
		return
	}
	m.errBar.text = ""
	names, index := m.accountChoices(entry.sess.Account)
	m.account = accountState{sess: entry.sess, names: names, index: index}
	_, m.account.migrate = migrate.AccountSwitchTranscript(migrateRoots(), tool, entry.sess)
	m.mode = modeAccount
}

// accountChoices keeps the current account selectable even when listing fails.
func (m *Model) accountChoices(current string) ([]string, int) {
	names := []string{ownLogin}
	seen := map[string]bool{}
	for _, toolName := range sortedToolNames(m.cfg) {
		tool := m.cfg.Tools[toolName]
		if tool.AccountEnv == "" {
			continue
		}
		listed, err := accounts.List(tool)
		if err != nil {
			m.errBar.text = "listing " + toolName + " accounts: " + err.Error()
			continue
		}
		if len(listed) == 0 {
			m.errBar.text = "listing " + toolName + " accounts: none found by " + tool.AccountsCommand
		}
		for _, name := range listed {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	if current != "" && !seen[current] {
		names = append(names, current)
	}
	index := 0
	for i, name := range names {
		if name == current && current != "" {
			index = i
		}
	}
	return names, index
}

func (m *Model) handleAccountKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.account.names)
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.errBar.text = ""
	case "left", "h", "shift+tab", "up", "k":
		m.account.index = (m.account.index - 1 + n) % n
	case "right", "l", "tab", "down", "j":
		m.account.index = (m.account.index + 1) % n
	case "enter":
		return m.submitAccountSwitch()
	}
	return m, nil
}

// Large contexts migrate so changing subscriptions does not replay the full context.
func (m *Model) submitAccountSwitch() (tea.Model, tea.Cmd) {
	sess, err := m.store.Get(m.account.sess.ID)
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	account := m.account.names[m.account.index]
	if account == ownLogin {
		account = ""
	}
	m.mode = modeList
	if account == sess.Account {
		m.errBar.text = sess.Name + " is already on " + m.account.names[m.account.index]
		return m, nil
	}
	account, err = launch.AccountForSwitch(m.cfg.Tools[sess.Tool], sess.TmuxPaneID != "", account)
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	transcript, large := migrate.AccountSwitchTranscript(migrateRoots(), m.cfg.Tools[sess.Tool], sess)
	if m.account.migrate && transcript.Path == "" {
		m.errBar.text = "cannot read current context; account unchanged"
		return m, nil
	}
	if large {
		name := textField("session name", 60)
		name.SetValue(sess.Name + "-" + sess.Tool)
		m.migrate = migrateState{source: sess, name: name, toolNames: []string{sess.Tool}, transcript: transcript, account: &account}
		return m.submitMigrate()
	}
	if err := m.store.SetAccount(sess.ID, account); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	sess.Account = account
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].Account = account
		}
	}
	if m.tmux.Exists(sess.ID) {
		if err := m.resumeSession(sess); err != nil {
			m.reportLaunchError(err)
			return m, nil
		}
	}
	m.errBar.text = ""
	m.rebuildRows()
	m.requestRefresh()
	return m, nil
}

func (m *Model) viewAccountSwitch() string {
	sess := m.account.sess
	now := ownLogin
	if sess.Account != "" {
		now = sess.Account
	}
	then := "takes effect on its next revive"
	if m.tmux.Exists(sess.ID) {
		then = "restarts it on the conversation it is on"
	}
	if m.account.migrate {
		then = "over 200k context: migrate to new session"
	}
	body := "  session  " + valueStyle.Render(sess.Name) + "  " + mutedStyle.Render("on "+sess.Tool+" as "+now) + "\n" +
		"  account  " + subtleStyle.Render("◂ ") + valueStyle.Render(m.account.names[m.account.index]) + subtleStyle.Render(" ▸") + "\n" +
		"           " + mutedStyle.Render(then)
	return m.card("⇄ Switch Account", body, [][2]string{{"←→", "account"}, {"↵", "switch"}, {"esc", "cancel"}})
}
