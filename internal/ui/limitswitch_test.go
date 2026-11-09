package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
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
	m.cfg.Board.OfferCLIOnLimit = true
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeLimitSwitch || m.limitSwitch.targets[m.limitSwitch.index] != subscriptionTarget {
		t.Fatalf("the opted-in card did not default to another subscription: mode=%v offer=%+v", m.mode, m.limitSwitch)
	}
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeMigrate || m.migrateTool() != "codex" {
		t.Fatalf("did not open migration review: mode=%v target=%q", m.mode, m.migrateTool())
	}
	rows, err := m.store.ListSessions(true)
	if err != nil || len(rows) != 1 {
		t.Fatalf("review launched a session: rows=%+v err=%v", rows, err)
	}
}

// Moving to another CLI is opt-in: by default a limit offers another
// subscription of the same CLI and nothing else, and Enter reviews that.
func TestLimitSwitchNeverOffersCodexByDefault(t *testing.T) {
	m := limitedModel(t)
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeLimitSwitch {
		t.Fatalf("no card: mode=%v", m.mode)
	}
	if !slices.Equal(m.limitSwitch.targets, []string{subscriptionTarget}) {
		t.Fatalf("default card offers %q, want only another subscription", m.limitSwitch.targets)
	}
	if view := ansi.Strip(m.viewLimitSwitch()); strings.Contains(view, "codex") {
		t.Fatalf("default card names codex:\n%s", view)
	}
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeAccount || m.account.sess.ID != sess.ID {
		t.Fatalf("enter did not open the subscription review: mode=%v", m.mode)
	}
}

// Claude's limit menu leaves no input line on the screen. The card still
// opens, and its banner is the limit line alone, not the reply, lists and
// table above it flattened into one paragraph.
func TestLimitSwitchBannerUnderTheLimitMenuIsTheLimitLine(t *testing.T) {
	m := limitedModel(t)
	sess, _ := m.selected()
	pane := "  - Raise the size limits before the first run.\n" +
		"  │ Step         │ The tool       │\n" +
		"\n" +
		"● User answered Claude's questions:\n" +
		"  ⎿  You've hit your weekly limit · resets Friday at 9pm\n" +
		"\n" +
		"   What do you want to do?\n" +
		"\n" +
		"   ❯ 1. Stop and wait for limit to reset\n" +
		"     2. Switch to usage credits\n"
	m.Update(previewMsg{sessID: sess.ID, preview: pane})
	if m.mode != modeLimitSwitch {
		t.Fatalf("no card: mode=%v", m.mode)
	}
	view := ansi.Strip(m.viewLimitSwitch())
	if !strings.Contains(view, "You've hit your weekly limit · resets Friday at 9pm") ||
		strings.Contains(view, "size limits") || strings.Contains(view, "The tool") {
		t.Fatalf("the banner carries more than the limit line:\n%s", view)
	}
}

// A CLI with no accounts has nothing to switch to, so without the opt-in
// a limit opens no card at all.
func TestLimitSwitchStaysClosedWithNothingToOffer(t *testing.T) {
	m := limitedModel(t)
	tool := m.cfg.Tools["claude"]
	tool.AccountEnv = ""
	m.cfg.Tools["claude"] = tool
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeList {
		t.Fatalf("card opened with nothing to offer: %+v", m.limitSwitch)
	}
}

// With the build's chooser routing accounts, the extension moves a limited
// session in the background, so the board does not open a card that would
// race it; the opt-in to other CLIs still asks.
func TestLimitSwitchLeavesRoutedAccountsToTheExtension(t *testing.T) {
	chooser := &accountstest.Chooser{}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	m := limitedModel(t)
	if err := m.store.SetSetting(store.AccountRoutingSetting, accounts.Extension); err != nil {
		t.Fatal(err)
	}
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeList {
		t.Fatalf("card opened while the extension routes accounts: %+v", m.limitSwitch)
	}

	m.cfg.Board.OfferCLIOnLimit = true
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeLimitSwitch || !slices.Contains(m.limitSwitch.targets, "codex") {
		t.Fatalf("opted-in card did not offer other CLIs: mode=%v offer=%+v", m.mode, m.limitSwitch)
	}
	if m.limitSwitch.targets[0] != subscriptionTarget {
		t.Fatalf("a CLI was put ahead of the subscription: %q", m.limitSwitch.targets)
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
	m.noteFocusActivity()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane, chase: true, echoed: true})
	if m.mode != modeFocus {
		t.Fatal("limit offer interrupted typing once the key echoed")
	}
	m.focusTypedAt = time.Time{}
	m.Update(previewMsg{sessID: sess.ID, skipped: true})
	if m.mode != modeLimitSwitch {
		t.Fatal("limit offer did not open after typing stopped")
	}
}

func TestLimitSwitchDefersBehindReviewsTab(t *testing.T) {
	m := limitedModel(t)
	sess, _ := m.selected()
	m.reviewQueue, m.reviewsTab = true, true
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeList || !m.onReviewsTab() {
		t.Fatal("limit offer replaced the Reviews tab")
	}
	m.reviewsTab = false
	m.Update(previewMsg{sessID: sess.ID, skipped: true})
	if m.mode != modeLimitSwitch {
		t.Fatal("deferred offer was lost")
	}
}

// relaunchLimited stands in for a restart of every row onto account: a new
// launch whose pane still replays the conversation's old limit banner.
func relaunchLimited(m *Model, account string, at time.Time) {
	for i := range m.sessions {
		m.sessions[i].AgentLaunchedAt = at
		m.sessions[i].Account = account
		m.sessions[i].Status = status.Errored
	}
	m.rebuildRows()
}

// A relaunch onto another account replays the banner the old account hit;
// that banner is not a new limit and must not reopen the card.
func TestLimitSwitchIgnoresTheOldAccountsBannerAfterAMove(t *testing.T) {
	m := limitedModel(t)
	relaunchLimited(m, "LOGAN1", time.Now().Add(-time.Hour))
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeLimitSwitch {
		t.Fatal("the first limit was not offered")
	}
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	relaunchLimited(m, "ASHBIN1", time.Now())
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeList {
		t.Fatalf("the replayed banner reopened the card after the move: mode %v", m.mode)
	}

	// A different banner on the new account is a new limit.
	m.Update(previewMsg{sessID: sess.ID, preview: "You've hit your session limit · resets 6pm\n❯ "})
	if m.mode != modeLimitSwitch {
		t.Fatal("a new limit on the new account was not offered")
	}
}

// A relaunch on the same account that still shows the limit is the limit
// holding, and is offered again as before.
func TestLimitSwitchReoffersARelaunchOnTheSameAccount(t *testing.T) {
	m := limitedModel(t)
	relaunchLimited(m, "LOGAN1", time.Now().Add(-time.Hour))
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	relaunchLimited(m, "LOGAN1", time.Now())
	m.Update(previewMsg{sessID: sess.ID, preview: weeklyLimitPane})
	if m.mode != modeLimitSwitch {
		t.Fatalf("the limit on a same-account relaunch was not offered: mode %v", m.mode)
	}
}
