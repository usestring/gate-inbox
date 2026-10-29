package ui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
)

const weeklyLimitPane = "You've hit your weekly limit · resets Friday at 9pm\n❯ "

func limitedModel(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	seedMigrateSource(t, m)
	tool := m.cfg.Tools["claude"]
	tool.ActivityCutoff = "(?m)^❯"
	tool.LimitLine = "(?m)You've hit your .+limit"
	tool.AccountEnv = "DEMO_ACCOUNT"
	m.cfg.Tools["claude"] = tool
	m.cfg.Tools["codex"] = config.Tool{Command: "cat", DefaultStatus: status.Idle}
	var err error
	m.engine, err = status.NewEngine(m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.sessions {
		m.sessions[i].Status = status.Errored
	}
	m.rebuildRows()
	return m
}

func TestLimitSwitchOffersReviewWithoutLaunching(t *testing.T) {
	m := limitedModel(t)
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeLimitSwitch || m.limitSwitch.targets[m.limitSwitch.index] != "codex" {
		t.Fatalf("mode=%v offer=%+v", m.mode, m.limitSwitch)
	}
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeMigrate || m.migrateTool() != "codex" {
		t.Fatalf("did not open migration review: mode=%v target=%q", m.mode, m.migrateTool())
	}
	rows, err := m.store.ListSessions(true)
	if err != nil || len(rows) != 1 {
		t.Fatalf("review launched a session: rows=%+v err=%v", rows, err)
	}
}

func TestLimitSwitchDismissalAndRecovery(t *testing.T) {
	m := limitedModel(t)
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.frameUnchanged()
	m.Update(previewMsg{sessID: sess.ID, skipped: true})
	if m.mode != modeList {
		t.Fatal("dismissed limit offered again")
	}
	for i := range m.sessions {
		m.sessions[i].Status = status.Working
	}
	m.rebuildRows()
	m.Update(previewMsg{sessID: sess.ID, preview: "● Working\n❯ "})
	for i := range m.sessions {
		m.sessions[i].Status = status.Errored
	}
	m.rebuildRows()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeLimitSwitch || m.frameReuse {
		t.Fatal("new limit episode did not paint its offer")
	}
}

func TestLimitSwitchPreservesOtherScreens(t *testing.T) {
	for _, screen := range []mode{modeForm, modeMigrate, modeQuickActions, modeAccount} {
		t.Run(fmt.Sprint(screen), func(t *testing.T) {
			m := limitedModel(t)
			sess, _ := m.selected()
			m.mode = screen
			m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
			if m.mode != screen {
				t.Fatalf("limit replaced screen %v", screen)
			}
			m.mode = modeList
			m.Update(previewMsg{sessID: sess.ID, skipped: true})
			if m.mode != modeLimitSwitch {
				t.Fatal("deferred offer was lost")
			}
		})
	}
}

func TestLimitSwitchSubscriptionAndMigrationShortcuts(t *testing.T) {
	m := limitedModel(t)
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	m.keys, _ = m.km().Rebind(keymap.ContextLimitSwitch, keymap.SwitchAccount, []string{"s"})
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if m.mode != modeLimitSwitch {
		t.Fatal("subscription choice kept its old key after rebinding")
	}
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if m.mode != modeAccount || m.account.sess.ID != sess.ID {
		t.Fatal("subscription choice did not open its review")
	}
	m.mode = modeList
	m.handleKey(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if m.mode != modeMigrate {
		t.Fatalf("migration shortcut did not open review: %s", m.errBar.text)
	}
	m.mode = modeList
	m.handleKey(tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt})
	if m.mode != modeAccount {
		t.Fatal("subscription shortcut did not open review")
	}
	if action, _ := m.km().Action(keymap.ContextList, "M"); action != keymap.Mute {
		t.Fatal("migration took the mute shortcut")
	}
}

func TestLimitSwitchDefersWhileTypingInFocus(t *testing.T) {
	m := limitedModel(t)
	sess, _ := m.selected()
	m.mode = modeFocus
	m.focusActiveAt = time.Now().Add(time.Hour)
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeFocus {
		t.Fatal("limit offer interrupted typing")
	}
	m.focusActiveAt = time.Time{}
	m.Update(previewMsg{sessID: sess.ID, skipped: true})
	if m.mode != modeLimitSwitch {
		t.Fatal("limit offer did not open after typing stopped")
	}
}
