package ui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

type limitSwitchState struct {
	source  store.Session
	banner  string
	targets []string
	index   int
	account bool
}

// limitSeen is the limit banner a session's card was last offered for:
// the launch it showed on, the account that launch ran on, and its text.
type limitSeen struct {
	launch  time.Time
	account string
	banner  string
}

// covers reports whether the card already offered for seen answers the
// banner now showing. The same banner on the same launch is the same
// episode. A relaunch replays the conversation, banner included, so once
// the session has moved to another account since, the same text is the
// old account's limit scrolled back into view, not a new one; a relaunch
// on the same account that still shows it is the limit holding.
func (seen limitSeen) covers(sess store.Session, banner string) bool {
	if seen.banner != banner {
		return false
	}
	return seen.launch.Equal(sess.LaunchTime()) || seen.account != sess.Account
}

func (m *Model) offerLimitSwitch(sess store.Session) tea.Cmd {
	if (m.mode != modeList && m.mode != modeFocus) || m.searching || m.quick.active ||
		m.legendPeek.visible || m.focusScroll != 0 || (m.mode == modeFocus && m.focusTyping()) ||
		(m.mode == modeList && m.onReviewsTab()) ||
		sess.Archived || sess.Muted || m.engine == nil {
		return nil
	}
	seen, offered := m.limitSwitchSeen[sess.ID]
	if sess.Status != status.Errored && !offered {
		return nil
	}
	pane := ansi.Strip(m.preview)
	banner := m.engine.LimitBanner(sess.Tool, pane)
	if banner == "" {
		if sess.Status == status.Working || sess.Status == status.Idle || sess.Status == status.Finished {
			delete(m.limitSwitchSeen, sess.ID)
		}
		return nil
	}
	if sess.Status != status.Errored || m.engine.CapacityBanner(sess.Tool, pane) != "" {
		return nil
	}
	if offered && seen.covers(sess, banner) {
		// seen stays as recorded: the banner belongs to the account it
		// was first offered on, whichever launch shows it now.
		return nil
	}
	account := m.cfg.Tools[sess.Tool].AccountEnv != ""
	// With the build's chooser routing accounts, the extension that routes
	// them moves a limited session in the background, whichever session the
	// board is showing; a card here would only race it. The opt-in to other
	// CLIs still asks, since nothing moves a session to another CLI unasked.
	if account && !m.cfg.Board.OfferCLIOnLimit && m.accountsRouted() {
		return nil
	}
	targets := m.limitTargets(sess.Tool, account)
	if len(targets) == 0 {
		return nil
	}
	if m.limitSwitchSeen == nil {
		m.limitSwitchSeen = make(map[string]limitSeen)
	}
	m.limitSwitchSeen[sess.ID] = limitSeen{launch: sess.LaunchTime(), account: sess.Account, banner: banner}
	var cmd tea.Cmd
	if m.mode == modeFocus {
		cmd = m.leaveFocus()
	}
	m.limitSwitch = limitSwitchState{source: sess, banner: banner, targets: targets, account: account}
	m.mode = modeLimitSwitch
	m.frameReuse = false
	return cmd
}

// subscriptionTarget is the card's choice of another account of the same
// CLI, listed among the CLIs it can move to.
const subscriptionTarget = ""

// limitTargets is what the limit card offers, its default first: another
// subscription of the same CLI when the CLI has accounts, then, only when
// the operator opted in with [board] offer_cli_on_limit, every other enabled
// CLI in the operator's order. No CLI is ever promoted ahead of the
// subscription.
func (m *Model) limitTargets(tool string, account bool) []string {
	var targets []string
	if account {
		targets = append(targets, subscriptionTarget)
	}
	if m.cfg.Board.OfferCLIOnLimit {
		targets = append(targets, slices.DeleteFunc(m.enabledToolNames(), func(name string) bool { return name == tool })...)
	}
	return targets
}

// accountsRouted reports whether launches take their account from the
// build's chooser: an extension is routing accounts, so it owns moving a
// limited session too.
func (m *Model) accountsRouted() bool {
	if m.store == nil || !accounts.ChooserAvailable() {
		return false
	}
	mode, err := accounts.Mode(m.store)
	return err == nil && mode == accounts.Extension
}

func (m *Model) handleLimitSwitchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if action, ok := m.action(keymap.ContextLimitSwitch, msg); ok && action == keymap.SwitchAccount {
		return m.reviewLimitSwitch(true)
	}
	switch msg.String() {
	case "esc", "q":
		m.mode = modeList
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "tab", "right", "shift+tab", "left":
		if n := len(m.limitSwitch.targets); n > 0 {
			delta := 1
			if msg.String() == "shift+tab" || msg.String() == "left" {
				delta = -1
			}
			m.limitSwitch.index = (m.limitSwitch.index + delta + n) % n
		}
	case "enter":
		s := m.limitSwitch
		return m.reviewLimitSwitch(s.targets[s.index] == subscriptionTarget)
	}
	return m, nil
}

func (m *Model) reviewLimitSwitch(account bool) (tea.Model, tea.Cmd) {
	if account && !m.limitSwitch.account {
		return m, nil
	}
	m.mode = modeList
	if !m.focusSession(m.limitSwitch.source.ID) {
		m.errBar.text = "the limited session is no longer listed"
		return m, nil
	}
	if account {
		return m, m.openAccountSwitch()
	}
	cmd := m.openMigrate()
	if m.mode == modeMigrate {
		target := m.limitSwitch.targets[m.limitSwitch.index]
		index := slices.Index(m.migrate.toolNames, target)
		if index < 0 {
			m.errBar.text = target + " is no longer available: tab to choose a CLI"
			return m, cmd
		}
		m.migrate.toolIndex = index
		m.migrate.name.SetValue(m.migrateProposedName())
		m.migrate.name.CursorEnd()
	}
	return m, cmd
}

func (m *Model) viewLimitSwitch() string {
	s := m.limitSwitch
	body := valueStyle.Render(s.source.Name) + mutedStyle.Render(" on "+s.source.Tool) + "\n\n" +
		s.banner + "\n\n"
	keys := [][2]string{{"esc", "later"}}
	if s.targets[s.index] == subscriptionTarget {
		body += "Switch to another " + s.source.Tool + " subscription and resume this conversation?\n" +
			mutedStyle.Render("Review the switch before the session restarts.")
	} else {
		body += "Continue this conversation on " + valueStyle.Render(s.targets[s.index]) + "?\n" +
			mutedStyle.Render("Review the move before starting the new session.")
	}
	if len(s.targets) > 1 {
		keys = append([][2]string{{"tab", "other choice"}}, keys...)
	}
	keys = append([][2]string{{"↵", "review"}}, keys...)
	if s.account && s.targets[s.index] != subscriptionTarget {
		keys = append(keys, [2]string{m.cap(keymap.ContextLimitSwitch, keymap.SwitchAccount), "subscription"})
	}
	return m.card("Usage limit reached", ansi.Wrap(body, cardInnerWidth(m.cardWidth()), ""), keys)
}
