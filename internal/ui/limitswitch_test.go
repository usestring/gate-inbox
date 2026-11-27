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
	"github.com/usestring/gate-inbox/internal/limitepisode"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
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
	m.poller.engine = m.engine
	for i := range m.sessions {
		m.sessions[i].Status = status.Errored
	}
	m.rebuildRows()
	return m
}

// showLimit is one poll of the selected session's pane followed by the
// frame landing: the pass records the limit episode the pane shows, which
// the card then reads.
func showLimit(t *testing.T, m *Model, pane string) {
	t.Helper()
	observeLimit(t, m, pane)
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, preview: pane})
}

// observeLimit is the poll alone, with no frame reaching the card: the
// session is not the one on screen, or another card is up.
func observeLimit(t *testing.T, m *Model, pane string) {
	t.Helper()
	sess, _ := m.selected()
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Status = sess.Status
	states, err := m.store.LimitRecoveries()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.poller.maybeRecoverLimit(stored, states, tmux.Capture{Text: pane}, ansi.Strip(pane), sess.Status, true, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	m.limitEpisodes = states
}

// setStatus is a pass reading every row at st.
func setRowsStatus(m *Model, st string) {
	for i := range m.sessions {
		m.sessions[i].Status = st
	}
	m.rebuildRows()
}

func TestLimitSwitchOffersReviewWithoutLaunching(t *testing.T) {
	m := limitedModel(t)
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeLimitSwitch || m.limitSwitch.targets[m.limitSwitch.index] != subscriptionTarget {
		t.Fatalf("the card did not default to another subscription: mode=%v offer=%+v", m.mode, m.limitSwitch)
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

// Moving to another CLI is opt-out: with offer_cli_on_limit = false a limit
// offers another subscription of the same CLI and nothing else, and Enter
// reviews that.
func TestLimitSwitchOffersOnlySubscriptionWhenCLIsTurnedOff(t *testing.T) {
	m := limitedModel(t)
	off := false
	m.cfg.Board.OfferCLIOnLimit = &off
	sess, _ := m.selected()
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeLimitSwitch {
		t.Fatalf("no card: mode=%v", m.mode)
	}
	if !slices.Equal(m.limitSwitch.targets, []string{subscriptionTarget}) {
		t.Fatalf("card offers %q, want only another subscription", m.limitSwitch.targets)
	}
	if view := ansi.Strip(m.viewLimitSwitch()); strings.Contains(view, "codex") {
		t.Fatalf("card names codex:\n%s", view)
	}
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.mode != modeAccount || m.account.sess.ID != sess.ID {
		t.Fatalf("enter did not open the subscription review: mode=%v", m.mode)
	}
}

// By default the card leads with another subscription and lists the other
// CLIs after it.
func TestLimitSwitchDefaultsToSubscriptionThenOtherCLIs(t *testing.T) {
	m := limitedModel(t)
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeLimitSwitch || len(m.limitSwitch.targets) < 2 ||
		m.limitSwitch.targets[0] != subscriptionTarget || !slices.Contains(m.limitSwitch.targets, "codex") {
		t.Fatalf("card offers %q, want the subscription first and then codex", m.limitSwitch.targets)
	}
	if m.limitSwitch.index != 0 {
		t.Fatalf("card selected %d, want the subscription", m.limitSwitch.index)
	}
}

// Claude's limit menu leaves no input line on the screen. The card still
// opens, and its banner is the limit line alone, not the reply, lists and
// table above it flattened into one paragraph.
func TestLimitSwitchBannerUnderTheLimitMenuIsTheLimitLine(t *testing.T) {
	m := limitedModel(t)
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
	showLimit(t, m, pane)
	if m.mode != modeLimitSwitch {
		t.Fatalf("no card: mode=%v", m.mode)
	}
	view := ansi.Strip(m.viewLimitSwitch())
	if !strings.Contains(view, "You've hit your weekly limit · resets Friday at 9pm") ||
		strings.Contains(view, "size limits") || strings.Contains(view, "The tool") {
		t.Fatalf("the banner carries more than the limit line:\n%s", view)
	}
}

// Submitting to an account that is already spent opens Claude's limit menu
// with no banner for the turn; the only banner on screen belongs to the
// account the session was moved off. The card still opens, on the menu's own
// "continue automatically" row rather than that stale banner.
func TestLimitSwitchOpensOnTheLimitMenuWithoutABanner(t *testing.T) {
	m := limitedModel(t)
	def, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	tool := m.cfg.Tools["claude"]
	tool.LimitMenu = def.Tools["claude"].LimitMenu
	m.cfg.Tools["claude"] = tool
	if m.engine, err = status.NewEngine(m.cfg); err != nil {
		t.Fatal(err)
	}
	m.poller.engine = m.engine
	pane := "  ⎿  You've hit your weekly limit · resets 12pm (UTC)\n" +
		"\n" +
		"❯ re-run the unit\n" +
		"\n" +
		"● Re-running the unit now.\n" +
		"\n" +
		"❯ [gate-inbox] Gate Inbox has moved this session onto account work-2. Carry on.\n" +
		strings.Repeat("▔", 60) + "\n" +
		"   What do you want to do?\n" +
		"\n" +
		"   ❯ 1. Stop and wait for limit to reset\n" +
		"     2. Wait here, then continue automatically at Nov 28, 12pm\n" +
		"\n" +
		"   Enter to confirm · Esc to cancel\n"
	if got, _ := m.engine.Match("claude", pane); got != status.Errored {
		t.Fatalf("Match() = %q, want %q", got, status.Errored)
	}
	showLimit(t, m, pane)
	if m.mode != modeLimitSwitch {
		t.Fatalf("no card: mode=%v", m.mode)
	}
	view := ansi.Strip(m.viewLimitSwitch())
	if !strings.Contains(view, "Wait here, then continue automatically at Nov 28,") || strings.Contains(view, "resets 12pm") {
		t.Fatalf("the card does not lead with the menu:\n%s", view)
	}
}

// A CLI with no accounts has nothing to switch to, so with other CLIs
// turned off a limit opens no card at all.
func TestLimitSwitchStaysClosedWithNothingToOffer(t *testing.T) {
	m := limitedModel(t)
	off := false
	m.cfg.Board.OfferCLIOnLimit = &off
	tool := m.cfg.Tools["claude"]
	tool.AccountEnv = ""
	m.cfg.Tools["claude"] = tool
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeList {
		t.Fatalf("card opened with nothing to offer: %+v", m.limitSwitch)
	}
}

// routeAccounts has the build's chooser routing accounts, so an extension
// is expected to move a limited session in the background.
func routeAccounts(t *testing.T, m *Model) {
	t.Helper()
	chooser := &accountstest.Chooser{}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	if err := m.store.SetSetting(store.AccountRoutingSetting, accounts.Extension); err != nil {
		t.Fatal(err)
	}
}

// ageLimit moves the selected session's episode back by d, as if the board
// had first seen the banner that long ago.
func ageLimit(t *testing.T, m *Model, d time.Duration) {
	t.Helper()
	sess, _ := m.selected()
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	ep := m.limitEpisodes[sess.ID]
	aged := ep
	aged.SeenAt = ep.SeenAt.Add(-d)
	if ok, err := m.store.CompareLimitRecovery(stored, &ep, &aged); err != nil || !ok {
		t.Fatalf("age episode: %v %v", ok, err)
	}
	m.limitEpisodes[sess.ID] = aged
}

// With the build's chooser routing accounts, an extension is expected to
// move a session whose turn the limit cut off, so the card holds back for
// limitepisode.MoveGrace rather than race it. A session still limited after
// that was not moved, and the card offers another subscription, which is
// the way off the account, ahead of the other CLIs.
func TestLimitSwitchHoldsBackForTheExtensionThenOffersTheSubscription(t *testing.T) {
	m := limitedModel(t)
	routeAccounts(t, m)
	sess, _ := m.selected()
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeList {
		t.Fatalf("card opened while the extension is moving the session: %+v", m.limitSwitch)
	}
	ageLimit(t, m, limitepisode.MoveGrace)
	m.Update(previewMsg{sessID: sess.ID, skipped: true})
	if m.mode != modeLimitSwitch {
		t.Fatalf("no card once the move was overdue: mode=%v", m.mode)
	}
	if m.limitSwitch.targets[0] != subscriptionTarget || !m.limitSwitch.account || !slices.Contains(m.limitSwitch.targets, "codex") {
		t.Fatalf("card offers %q, want another subscription first, then codex", m.limitSwitch.targets)
	}
}

// A pane the board adopted rather than started is offered another
// subscription too, which takes the pane over onto the board as it moves.
func TestLimitSwitchOffersTheSubscriptionForAnAdoptedPane(t *testing.T) {
	m := limitedModel(t)
	routeAccounts(t, m)
	for i := range m.sessions {
		m.sessions[i].TmuxPaneID = "%7"
	}
	m.rebuildRows()
	showLimit(t, m, weeklyLimitPane)
	ageLimit(t, m, limitepisode.MoveGrace)
	sess, _ := m.selected()
	m.Update(previewMsg{sessID: sess.ID, skipped: true})
	if m.mode != modeLimitSwitch || !slices.Contains(m.limitSwitch.targets, subscriptionTarget) {
		t.Fatalf("adopted pane was not offered another subscription: mode=%v offer=%+v", m.mode, m.limitSwitch)
	}
}

func TestLimitSwitchDismissalAndRecovery(t *testing.T) {
	m := limitedModel(t)
	sess, _ := m.selected()
	showLimit(t, m, weeklyLimitPane)
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.frameUnchanged()
	m.Update(previewMsg{sessID: sess.ID, skipped: true})
	if m.mode != modeList {
		t.Fatal("dismissed limit offered again")
	}
	setRowsStatus(m, status.Working)
	showLimit(t, m, "● Working\n❯ ")
	setRowsStatus(m, status.Errored)
	showLimit(t, m, weeklyLimitPane)
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
			showLimit(t, m, weeklyLimitPane)
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
	showLimit(t, m, weeklyLimitPane)
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
	if action, _ := m.km().Action(keymap.ContextList, "ctrl+m"); action != keymap.Mute {
		t.Fatal("migration took the mute shortcut")
	}
}

func TestLimitSwitchDefersWhileTypingInFocus(t *testing.T) {
	m := limitedModel(t)
	sess, _ := m.selected()
	m.mode = modeFocus
	m.noteFocusActivity()
	observeLimit(t, m, weeklyLimitPane)
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
	showLimit(t, m, weeklyLimitPane)
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
func relaunchLimited(t *testing.T, m *Model, account string, at time.Time) {
	t.Helper()
	for _, sess := range m.sessions {
		if err := m.store.SetAgentLaunchedAt(sess.ID, at); err != nil {
			t.Fatal(err)
		}
		if err := m.store.SetAccount(sess.ID, account); err != nil {
			t.Fatal(err)
		}
	}
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
	relaunchLimited(t, m, "LOGAN1", time.Now().Add(-time.Hour))
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeLimitSwitch {
		t.Fatal("the first limit was not offered")
	}
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	relaunchLimited(t, m, "ASHBIN1", time.Now())
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeList {
		t.Fatalf("the replayed banner reopened the card after the move: mode %v", m.mode)
	}

	// A different banner on the new account is a new limit.
	showLimit(t, m, "You've hit your session limit · resets 6pm\n❯ ")
	if m.mode != modeLimitSwitch {
		t.Fatal("a new limit on the new account was not offered")
	}
}

// A relaunch on the same account that still shows the limit is the limit
// holding, and is offered again as before.
func TestLimitSwitchReoffersARelaunchOnTheSameAccount(t *testing.T) {
	m := limitedModel(t)
	relaunchLimited(t, m, "LOGAN1", time.Now().Add(-time.Hour))
	showLimit(t, m, weeklyLimitPane)
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	relaunchLimited(t, m, "LOGAN1", time.Now())
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeLimitSwitch {
		t.Fatalf("the limit on a same-account relaunch was not offered: mode %v", m.mode)
	}
}

// A relaunch's pane is blank while the agent starts, and the row reads
// working until the conversation is drawn again. That frame is no sign the
// episode is over: the replayed banner after it is still the old account's.
func TestLimitSwitchStaysClosedThroughTheRelaunchsBlankPane(t *testing.T) {
	m := limitedModel(t)
	relaunchLimited(t, m, "WORK1", time.Now().Add(-time.Hour))
	showLimit(t, m, weeklyLimitPane)
	m.handleLimitSwitchKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	relaunchLimited(t, m, "WORK2", time.Now())
	setRowsStatus(m, status.Working)
	showLimit(t, m, "")
	setRowsStatus(m, status.Errored)
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeList {
		t.Fatalf("the replayed banner reopened the card after a move: mode %v", m.mode)
	}
}

// A session moved to another account by hand before the card was ever
// offered (the account card, switch_account, an extension) shows the old
// account's banner again once it relaunches, and that is no new limit.
func TestLimitSwitchStaysClosedAfterAMoveItNeverOffered(t *testing.T) {
	m := limitedModel(t)
	relaunchLimited(t, m, "WORK1", time.Now().Add(-time.Hour))
	m.mode = modeForm
	observeLimit(t, m, weeklyLimitPane)
	m.mode = modeList

	relaunchLimited(t, m, "WORK2", time.Now())
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeList {
		t.Fatalf("the replayed banner opened the card after a hand move: mode %v", m.mode)
	}
}

// Once the conversation has been migrated to another session the source is
// no longer the operator's to rescue, though its pane still shows the limit.
func TestLimitSwitchStaysClosedOnceTheConversationMigratedAway(t *testing.T) {
	m := limitedModel(t)
	source, _ := m.selected()
	m.mode = modeForm
	observeLimit(t, m, weeklyLimitPane)
	m.mode = modeList

	moved := store.Session{ID: "migrated", Name: "source-codex", Tool: "codex", Cwd: source.Cwd, Status: status.Working,
		MigrationID: source.ID, CreatedAt: time.Now().Add(time.Second)}
	if err := m.store.CreateSession(moved); err != nil {
		t.Fatal(err)
	}
	var err error
	if m.sessions, err = m.store.ListSessions(true); err != nil {
		t.Fatal(err)
	}
	for i := range m.sessions {
		if m.sessions[i].ID == source.ID {
			m.sessions[i].Status = status.Errored
		}
	}
	m.rebuildRows()
	if !m.focusSession(source.ID) {
		t.Fatal("source not listed")
	}
	showLimit(t, m, weeklyLimitPane)
	if m.mode != modeList {
		t.Fatalf("the card offered a move for a conversation already migrated: mode %v", m.mode)
	}
}
