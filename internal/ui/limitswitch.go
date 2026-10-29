package ui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

func (m *Model) offerLimitSwitch(sess store.Session) tea.Cmd {
	if (m.mode != modeList && m.mode != modeFocus) || m.searching || m.quick.active ||
		m.legendPeek.visible || m.focusScroll != 0 || (m.mode == modeFocus && m.focusActive()) ||
		sess.Archived || sess.Muted || m.engine == nil {
		return nil
	}
	if sess.Status != status.Errored && m.limitSwitchSeen[sess.ID] == "" {
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
	signature := sess.LaunchTime().Format(time.RFC3339Nano) + "\n" + banner
	if m.limitSwitchSeen[sess.ID] == signature {
		return nil
	}
	targets := slices.DeleteFunc(m.enabledToolNames(), func(name string) bool { return name == sess.Tool })
	if i := slices.Index(targets, "codex"); i > 0 {
		targets[0], targets[i] = targets[i], targets[0]
	}
	account := m.cfg.Tools[sess.Tool].AccountEnv != ""
	if len(targets) == 0 && !account {
		return nil
	}
	if m.limitSwitchSeen == nil {
		m.limitSwitchSeen = make(map[string]string)
	}
	m.limitSwitchSeen[sess.ID] = signature
	var cmd tea.Cmd
	if m.mode == modeFocus {
		cmd = m.leaveFocus()
	}
	m.limitSwitch = limitSwitchState{source: sess, banner: banner, targets: targets, account: account}
	m.mode = modeLimitSwitch
	m.frameReuse = false
	return cmd
}

func (m *Model) handleLimitSwitchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "enter", "a":
		account := msg.String() == "a" || len(m.limitSwitch.targets) == 0
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
	return m, nil
}

func (m *Model) viewLimitSwitch() string {
	s := m.limitSwitch
	body := valueStyle.Render(s.source.Name) + mutedStyle.Render(" on "+s.source.Tool) + "\n\n" +
		s.banner + "\n\n"
	keys := [][2]string{{"esc", "later"}}
	if len(s.targets) > 0 {
		body += "Continue this conversation on " + valueStyle.Render(s.targets[s.index]) + "?\n" +
			mutedStyle.Render("Review the move before starting the new session.")
		keys = append([][2]string{{"↵", "review move"}, {"tab", "CLI"}}, keys...)
	}
	if s.account {
		body += "\n\nOr switch to another " + s.source.Tool + " subscription."
		keys = append(keys, [2]string{"a", "subscription"})
	}
	return m.card("Usage limit reached", ansi.Wrap(body, cardInnerWidth(m.cardWidth()), ""), keys)
}
