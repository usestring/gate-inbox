package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/migrate"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// accountAuto heads the card's accounts when the build's extension chooses
// accounts, and leaves the account to it the way a migration does.
const accountAuto = "auto"

// accountState is the switch-account card: the session it is about and the
// accounts it may move to, "own login" first, or auto first when the build
// chooses accounts.
type accountState struct {
	sess    store.Session
	names   []string
	index   int
	migrate bool
	cutoff  int
	// generation tells an auto answer for this card from one for a card
	// since closed. autoPick and autoReason are the chooser's last answer,
	// autoAnswered whether there has been one; autoSubmit is set while
	// enter waits on one.
	generation   uint64
	autoPick     string
	autoReason   string
	autoAnswered bool
	autoSubmit   bool
}

// accountAutoMsg is the build's chooser's answer for the switch-account card.
type accountAutoMsg struct {
	generation uint64
	account    string
	err        error
	submit     bool
}

// openAccountSwitch opens the card for the session under the cursor. A
// token is read at launch, so moving a session needs a restart or migration.
func (m *Model) openAccountSwitch() tea.Cmd {
	entry, ok := m.selectedRow()
	if !ok {
		return nil
	}
	if !entry.isSession() {
		m.errBar.text = "select a session to switch its account"
		return nil
	}
	tool, ok := m.cfg.Tools[entry.sess.Tool]
	if !ok {
		m.errBar.text = fmt.Sprintf("tool %s is no longer configured", entry.sess.Tool)
		return nil
	}
	if tool.AccountEnv == "" {
		m.errBar.text = entry.sess.Tool + " cannot be launched on a chosen account (no account_env)"
		return nil
	}
	m.errBar.text = ""
	names, index := m.accountChoices(entry.sess.Account)
	if accounts.ChooserAvailable() && !slices.Contains(names, accountAuto) {
		names, index = append([]string{accountAuto}, names...), 0
	}
	m.account = accountState{generation: m.account.generation + 1, sess: entry.sess, names: names, index: index, cutoff: m.accountSwitchCutoff(tool, entry.sess)}
	_, m.account.migrate = migrate.AccountSwitchTranscript(migrateRoots(), tool, entry.sess, m.account.cutoff)
	m.mode = modeAccount
	return m.accountAutoCmd(false)
}

func (m *Model) accountOnAuto() bool {
	return m.account.names[m.account.index] == accountAuto
}

// accountAutoCmd asks the build's chooser, off the event loop, which account
// the card's auto entry moves the session to, through the routing a
// migration takes. Until submit it only previews, so a chooser that takes
// turns among accounts spends none on a card left unsubmitted. A card
// without auto asks nothing.
func (m *Model) accountAutoCmd(submit bool) tea.Cmd {
	if !slices.Contains(m.account.names, accountAuto) {
		return nil
	}
	sess := m.account.sess
	tool := m.cfg.Tools[sess.Tool]
	req := accounts.Request{SessionID: sess.ID, ToolName: sess.Tool, Reason: extension.LaunchRelaunch}
	generation := m.account.generation
	m.account.autoSubmit = submit
	return func() tea.Msg {
		msg := accountAutoMsg{generation: generation, submit: submit}
		if submit {
			msg.account, msg.err = accounts.Route(tool, req)
		} else {
			msg.account, msg.err = accounts.RoutePreview(tool, req)
		}
		return msg
	}
}

func (m *Model) finishAccountAuto(msg accountAutoMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeAccount || msg.generation != m.account.generation || msg.submit != m.account.autoSubmit {
		return m, nil
	}
	m.account.autoSubmit = false
	m.account.autoAnswered = true
	m.account.autoPick, m.account.autoReason = msg.account, ""
	sess := m.account.sess
	if current, err := m.store.Get(sess.ID); err == nil {
		sess = current
	}
	switch {
	case msg.err != nil:
		m.account.autoPick, m.account.autoReason = "", msg.err.Error()
	case msg.account == "":
		m.account.autoReason = "the chooser picked no account"
	// An adopted row never recorded its account, so only a managed row can
	// be known to be on the pick already.
	case msg.account == sess.Account && sess.TmuxPaneID == "":
		m.account.autoReason = "picks " + msg.account + ", the account " + sess.Name + " is already on"
	}
	if !msg.submit || !m.accountOnAuto() {
		return m, nil
	}
	if m.account.autoReason != "" {
		m.errBar.text = "auto: " + m.account.autoReason + ": choose an account"
		return m, nil
	}
	return m.switchAccountTo(msg.account)
}

// accountSwitchCutoff is sess's account-switch cutoff at its sizing tier,
// the default when the tier cannot be read.
func (m *Model) accountSwitchCutoff(tool config.Tool, sess store.Session) int {
	if m.store == nil {
		return config.DefaultAccountSwitchCutoff
	}
	tier, err := m.store.SizingTier(sess)
	if err != nil {
		return config.DefaultAccountSwitchCutoff
	}
	return tool.AccountSwitchCutoff(tier)
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
		m.account.autoSubmit = false
	case "right", "l", "tab", "down", "j":
		m.account.index = (m.account.index + 1) % n
		m.account.autoSubmit = false
	case "enter":
		// Auto switches to what the chooser commits to now, not to the
		// preview the card showed, so enter asks it again.
		if m.accountOnAuto() {
			if m.account.autoSubmit {
				return m, nil
			}
			return m, m.accountAutoCmd(true)
		}
		return m.submitAccountSwitch()
	}
	return m, nil
}

func (m *Model) submitAccountSwitch() (tea.Model, tea.Cmd) {
	return m.switchAccountTo(m.account.names[m.account.index])
}

// switchAccountTo moves the card's session onto label, an account name or
// ownLogin. Large contexts migrate so changing subscriptions does not replay
// the full context.
func (m *Model) switchAccountTo(label string) (tea.Model, tea.Cmd) {
	sess, err := m.store.Get(m.account.sess.ID)
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	account := label
	if account == ownLogin {
		account = ""
	}
	m.mode = modeList
	// An adopted row's account is whatever its pane was started on, which the
	// row never recorded, so only a managed row can already be on the pick.
	if account == sess.Account && sess.TmuxPaneID == "" {
		m.errBar.text = sess.Name + " is already on " + label
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
	transcript, large := migrate.AccountSwitchTranscript(migrateRoots(), m.cfg.Tools[sess.Tool], sess, m.account.cutoff)
	if m.account.migrate && transcript.Path == "" {
		m.errBar.text = "cannot read current context; account unchanged"
		return m, nil
	}
	if large {
		name := textField("session name", 60)
		name.SetValue(sess.Name + "-" + sess.Tool)
		m.migrate = migrateState{source: sess, name: name, toolNames: []string{sess.Tool}, transcript: transcript, account: &account,
			skip: sess.SkipPermissions, childrenSkip: sess.ChildrenSkipPermissions}
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
		then = fmt.Sprintf("over %dk context: migrate to new session", m.account.cutoff/1000)
	}
	var body strings.Builder
	body.WriteString("  session  " + valueStyle.Render(sess.Name) + "  " + mutedStyle.Render("on "+sess.Tool+" as "+now) + "\n")
	hint := [][2]string{{"↑↓", "account"}, {"↵", "switch"}, {"esc", "cancel"}}
	legendRows := strings.Count(legendInline(hint, cardInnerWidth(m.cardWidth())), "\n") + 1
	var reasonRows []string
	if m.accountOnAuto() {
		reasonRows = m.autoReasonRows(m.account.autoReason, len(accountListIndent)+2)
	}
	rows := m.height - 7 - legendRows - len(reasonRows)
	if m.errBar.text != "" {
		rows -= 2
	}
	rows = max(1, rows)
	start := max(0, m.account.index-rows+1)
	for i := start; i < min(len(m.account.names), start+rows); i++ {
		name := m.account.names[i]
		lead, marker, style := accountListIndent, "  ", mutedStyle
		if i == start {
			lead = "  account  "
		}
		if i == m.account.index {
			marker, style = keyStyle.Render("❯ "), valueStyle
		}
		body.WriteString(lead + marker + style.Render(name) + m.viewAccountAutoPick(name) + "\n")
	}
	for _, row := range reasonRows {
		body.WriteString(row + "\n")
	}
	body.WriteString(accountListIndent + mutedStyle.Render(then))
	return m.card("⇄ Switch Account", body.String(), hint)
}

// accountListIndent is the columns ahead of an account's marker on the card.
const accountListIndent = "           "

// viewAccountAutoPick is what follows auto's row: the account it would move
// the session to, once the chooser has said.
func (m *Model) viewAccountAutoPick(name string) string {
	if name != accountAuto {
		return ""
	}
	switch {
	case !m.account.autoAnswered:
		return " " + mutedStyle.Render("choosing…")
	case m.account.autoPick == "" || m.account.autoReason != "":
		return " " + mutedStyle.Render("no switch")
	}
	return " " + mutedStyle.Render("→ "+m.account.autoPick)
}
