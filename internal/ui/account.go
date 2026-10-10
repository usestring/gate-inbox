package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/status"
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
	// An adopted row's account is whatever its pane was started on, which the
	// row never recorded, so only a managed row can already be on the pick.
	if account == sess.Account && sess.TmuxPaneID == "" {
		m.errBar.text = sess.Name + " is already on " + m.account.names[m.account.index]
		return m, nil
	}
	relaunch := false
	if sess.TmuxPaneID != "" {
		var err error
		if sess, relaunch, err = m.takeOverPane(sess); err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
	}
	account, err = launch.AccountForSwitch(m.cfg.Tools[sess.Tool], false, account)
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
	if m.tmux.Exists(sess.ID) || relaunch {
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

// takeOverPane makes an adopted row the manager's own without asking: the
// conversation is read off the agent's process while it is still up, the
// pane is ended, and the row is promoted. It reports whether it ended a live
// pane, which is what decides whether the caller relaunches it: a pane that
// was already gone only needs the promotion, so a dead adopted row switches
// like any dead row. A relaunch that fails leaves the row dead and promoted,
// where v revives it: the pane is already gone, and a dead managed row is
// what park leaves too.
func (m *Model) takeOverPane(sess store.Session) (store.Session, bool, error) {
	tool, known := m.cfg.Tools[sess.Tool]
	if !known {
		return sess, false, fmt.Errorf("tool %s is no longer configured", sess.Tool)
	}
	if tool.Shell {
		return sess, false, fmt.Errorf("%s is a shell, not an agent", sess.Name)
	}
	promote := func(convID, cwd string) (store.Session, error) {
		if err := m.store.PromoteAdopted(sess.ID, cwd, convID); err != nil {
			return sess, err
		}
		promoted := sess
		promoted.TmuxSocket, promoted.TmuxPaneID = "", ""
		promoted.Cwd, promoted.AgentSessionID = cwd, convID
		promoted.Status = status.Dead
		for i := range m.sessions {
			if m.sessions[i].ID == sess.ID {
				m.sessions[i] = promoted
			}
		}
		return promoted, nil
	}
	if !m.tmux.Exists(sess.ID) {
		promoted, err := promote(sess.AgentSessionID, sess.Cwd)
		if err == nil {
			m.tmux.Release(sess.ID)
		}
		return promoted, false, err
	}
	convID, cwd := sess.AgentSessionID, sess.Cwd
	if sess.Tool == "claude" {
		if pid, err := m.tmux.PanePID(sess.ID); err == nil && pid != 0 {
			if session, ok := convo.ClaudeSessionInTree(
				convo.LiveClaudeSessions(convo.ClaudeHome()), adopt.NewProcTable().PIDs(int32(pid))); ok {
				convID = session.SessionID
				if session.Cwd != "" {
					cwd = session.Cwd
				}
			}
		}
	}
	// A tool that resumes by id needs the id: relaunching on its continue
	// command would resume the directory's most recent conversation, which
	// is the wrong one whenever panes share a checkout, and the pane would
	// already be gone. Better to leave it where it is and say so.
	if convID == "" && tool.ResumeByIDCommand != "" {
		return sess, false, fmt.Errorf("no conversation id could be read off its process, so %s stays in its pane", sess.Name)
	}
	if !isDir(cwd) {
		return sess, false, fmt.Errorf("working directory no longer exists: %s", cwd)
	}
	if err := m.endSession(sess, m.tmux.KillAdopted); err != nil {
		return sess, false, err
	}
	m.tmux.Release(sess.ID)
	promoted, err := promote(convID, cwd)
	return promoted, true, err
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
	var body strings.Builder
	body.WriteString("  session  " + valueStyle.Render(sess.Name) + "  " + mutedStyle.Render("on "+sess.Tool+" as "+now) + "\n")
	hint := [][2]string{{"↑↓", "account"}, {"↵", "switch"}, {"esc", "cancel"}}
	legendRows := strings.Count(legendInline(hint, cardInnerWidth(m.cardWidth())), "\n") + 1
	rows := m.height - 7 - legendRows
	if m.errBar.text != "" {
		rows -= 2
	}
	rows = max(1, rows)
	start := max(0, m.account.index-rows+1)
	for i := start; i < min(len(m.account.names), start+rows); i++ {
		name := m.account.names[i]
		lead, marker, style := "           ", "  ", mutedStyle
		if i == start {
			lead = "  account  "
		}
		if i == m.account.index {
			marker, style = keyStyle.Render("❯ "), valueStyle
		}
		body.WriteString(lead + marker + style.Render(name) + "\n")
	}
	body.WriteString("           " + mutedStyle.Render(then))
	return m.card("⇄ Switch Account", body.String(), hint)
}
