package ui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/limitepisode"
	"github.com/usestring/gate-inbox/internal/logging"
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

// offerLimitSwitch opens the limit card for sess, the selected session,
// when its limit episode calls for one (see limitepisode.Decide): once per
// episode, at once when nothing else will move the session, and only after
// limitepisode.MoveGrace when an extension is expected to move it in the
// background. The card always offers a way off the limited account: another
// subscription of the same CLI when it has accounts, then the other CLIs.
func (m *Model) offerLimitSwitch(sess store.Session) tea.Cmd {
	if (m.mode != modeList && m.mode != modeFocus) || m.searching || m.quick.active ||
		m.legendPeek.visible || m.focusScroll != 0 || (m.mode == modeFocus && m.focusTyping()) ||
		(m.mode == modeList && m.onReviewsTab()) ||
		sess.Archived || sess.Muted || sess.Status != status.Errored {
		return nil
	}
	ep, ok := m.limitEpisodes[sess.ID]
	if !ok {
		return nil
	}
	decision := m.limitDecision(sess, ep)
	if decision.CardAt.IsZero() || time.Now().Before(decision.CardAt) {
		return nil
	}
	targets := m.limitTargets(sess.Tool, decision.Subscription)
	if len(targets) == 0 {
		return nil
	}
	m.markLimitOffered(sess, ep)
	var cmd tea.Cmd
	if m.mode == modeFocus {
		cmd = m.leaveFocus()
	}
	m.limitSwitch = limitSwitchState{source: sess, banner: ep.Banner, targets: targets, account: decision.Subscription}
	if decision.Subscription {
		// Choosing another subscription opens the account card, which then
		// lists accounts as of now rather than as of its last opening.
		accounts.Prefetch(m.cfg.Tools[sess.Tool])
	}
	m.mode = modeLimitSwitch
	m.frameReuse = false
	return cmd
}

// limitDecision is limitepisode.Decide for one of the board's sessions.
func (m *Model) limitDecision(sess store.Session, ep store.LimitRecovery) limitepisode.Decision {
	return limitepisode.Decide(&ep, limitepisode.Facts{
		Session:   sess,
		Running:   sess.Status != status.Dead,
		Successor: limitepisode.Successor(m.sessions, sess, ep),
		Routed:    m.accountsRouted(),
		Accounts:  m.cfg.Tools[sess.Tool].AccountEnv != "",
	})
}

// markLimitOffered records that the card was shown for ep, so it is not
// offered again for the same episode, across a restart of the board too.
// A pass that changed the record meanwhile wins; the copy here still keeps
// this board from offering it twice.
func (m *Model) markLimitOffered(sess store.Session, ep store.LimitRecovery) {
	offered := ep
	offered.Offered = true
	m.limitEpisodes[sess.ID] = offered
	if m.store == nil {
		return
	}
	if _, err := m.store.CompareLimitRecovery(sess, &ep, &offered); err != nil {
		logging.Info("limit card: offer not recorded", "session", sess.ID, logging.Err(err))
	}
}

// subscriptionTarget is the card's choice of another account of the same
// CLI, listed among the CLIs it can move to.
const subscriptionTarget = ""

// limitTargets is what the limit card offers, its default first: another
// subscription of the same CLI when the CLI has accounts, then, unless
// [board] offer_cli_on_limit = false, every other enabled CLI (Codex,
// OpenCode and the rest) in the operator's order. No CLI is ever promoted
// ahead of the subscription.
func (m *Model) limitTargets(tool string, account bool) []string {
	var targets []string
	if account {
		targets = append(targets, subscriptionTarget)
	}
	if m.cfg.Board.OffersCLIOnLimit() {
		targets = append(targets, slices.DeleteFunc(m.enabledToolNames(), func(name string) bool { return name == tool })...)
	}
	return targets
}

// accountsRouted reports whether launches take their account from the
// build's chooser: an extension is routing accounts, so it is the one
// expected to move a limited session in the background.
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
