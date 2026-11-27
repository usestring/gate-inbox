package sessioncmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// These set the process's account chooser, so none of them is parallel.

// A migration takes no account from its caller and carries none from its
// source: with no chooser it runs on the CLI's own login.
func TestMigrateDoesNotCarryTheSourcesAccount(t *testing.T) {
	t.Cleanup(accounts.UseChooser(func() (extension.AccountChooser, error) { return nil, nil }))
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)
	if err := h.store.SetAccount(source.ID, "OLD"); err != nil {
		t.Fatal(err)
	}
	moved, err := h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "claude"})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if row, err := h.store.Get(moved.ID); err != nil || row.Account != "" {
		t.Fatalf("with no chooser the move should run on own login, got %q (%v)", row.Account, err)
	}
}

// With a chooser the migration runs on its pick, whatever the launch-account
// mode, and the chooser is told it is a migration and from where.
func TestMigrateAsksTheChooser(t *testing.T) {
	chooser := &accountstest.Chooser{Account: "alice1"}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	h := newSessionHarness(t)
	source, _ := claudeSource(t, h)
	moved, err := h.sessions.Migrate(h.caller.ID, source.ID, MigrateOptions{Tool: "claude"})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if moved.Account != "ALICE1" {
		t.Fatalf("migrated onto %q, want the chooser's ALICE1", moved.Account)
	}
	reqs := chooser.Requests()
	if len(reqs) != 1 || reqs[0].Reason != extension.LaunchMigrate || reqs[0].From != source.ID || reqs[0].SessionID != moved.ID || reqs[0].Preview {
		t.Fatalf("requests = %+v", reqs)
	}
}

// A spawn naming no account asks the chooser only in the extension mode; a
// named account is never the chooser's to change.
func TestCreateAsksTheChooserOnlyInItsMode(t *testing.T) {
	chooser := &accountstest.Chooser{Account: "alice1"}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	h := newSessionHarness(t)
	own, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "own", Tool: "claude"})
	if err != nil || own.Account != "" || len(chooser.Requests()) != 0 {
		t.Fatalf("own mode: %+v %v, asked %d", own, err, len(chooser.Requests()))
	}
	if err := h.store.SetSetting(store.AccountRoutingSetting, accounts.Extension); err != nil {
		t.Fatal(err)
	}
	chosen, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "chosen", Tool: "claude"})
	if err != nil || chosen.Account != "ALICE1" {
		t.Fatalf("extension mode: %+v %v", chosen, err)
	}
	named, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "named", Tool: "claude", Account: "bob2"})
	if err != nil || named.Account != "BOB2" || len(chooser.Requests()) != 1 {
		t.Fatalf("named: %+v %v, asked %d", named, err, len(chooser.Requests()))
	}
}

// BoardSwitchAccount re-points a session for a board extension, and refuses
// a context too large to resume rather than moving it to a new
// conversation.
func TestBoardSwitchAccount(t *testing.T) {
	h := newSessionHarness(t)
	sess := store.Session{ID: uuid.NewString()[:8], Name: "worker", Tool: "claude", Cwd: h.caller.Cwd, Group: h.caller.Group, Status: status.Dead, AgentSessionID: "conv-x"}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	account, tool, err := h.sessions.BoardAccount(sess.ID)
	if err != nil || account != "" || tool.Env != "CLAUDE_CODE_OAUTH_TOKEN" || tool.Secret == "" {
		t.Fatalf("BoardAccount = %q %+v %v", account, tool, err)
	}
	switched, err := h.sessions.BoardSwitchAccount(sess.ID, "alice1", false)
	if err != nil || switched.Account != "ALICE1" {
		t.Fatalf("BoardSwitchAccount = %+v %v", switched, err)
	}
	if account, _, _ := h.sessions.BoardAccount(sess.ID); account != "ALICE1" {
		t.Fatalf("account after the switch = %q", account)
	}

	big, transcript := claudeSource(t, h)
	raw := `{"type":"assistant","timestamp":"2026-09-01T00:00:00Z","message":{"model":"claude-fixture","usage":{"input_tokens":1001,"cache_read_input_tokens":198000,"cache_creation_input_tokens":1000}}}`
	if err := os.WriteFile(transcript, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.BoardSwitchAccount(big.ID, "alice1", false); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("a large context was not refused: %v", err)
	}
}

// SwitchAccount, behind switch_account and gate-inbox switch-account,
// refuses the caller's own session, since the relaunch would end the call,
// and a call from no session at all.
func TestSwitchAccountRefusesItsOwnSessionAndNoCaller(t *testing.T) {
	h := newSessionHarness(t)
	if _, err := h.sessions.SwitchAccount(h.caller.ID, h.caller.ID, "alice1"); err == nil || !strings.Contains(err.Error(), "cannot switch its own account") {
		t.Fatalf("a session switched its own account: %v", err)
	}
	if _, err := h.sessions.SwitchAccount("", h.caller.ID, "alice1"); err == nil {
		t.Fatal("a switch from no session was not refused")
	}
}

// A spawn named onto an account the build reports spent launches on the
// chooser's pick instead, and says so in its result; with no other account
// it is refused with the reset, and no session is left behind.
func TestCreateNamedOntoASpentAccount(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Minute)
	advisor := &accountstest.Advisor{
		Chooser: accountstest.Chooser{Account: "alice1"},
		Advice:  extension.Advice{Accounts: map[string]extension.AccountState{"BOB2": {Spent: true, Until: reset, Reason: "at its weekly limit"}}},
	}
	t.Cleanup(accounts.UseChooser(advisor.Resolve))
	h := newSessionHarness(t)
	moved, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "redirected", Tool: "claude", Account: "bob2"})
	if err != nil || moved.Account != "ALICE1" {
		t.Fatalf("Create = %+v %v, want it on ALICE1", moved, err)
	}
	if row, err := h.store.Get(moved.ID); err != nil || row.Account != "ALICE1" {
		t.Fatalf("row account = %q %v", row.Account, err)
	}
	if !strings.Contains(moved.AccountNote, "account BOB2 is spent until "+reset.Format("2006-01-02 15:04 UTC")) || !strings.Contains(moved.AccountNote, "launched on ALICE1 instead") {
		t.Fatalf("account note = %q", moved.AccountNote)
	}
	if line := FormatSession(moved); !strings.Contains(line, "as ALICE1") || !strings.Contains(line, "(account BOB2 is spent") {
		t.Fatalf("result line = %q", line)
	}
	reqs := advisor.Requests()
	if len(reqs) != 1 || len(reqs[0].Exclude) != 1 || reqs[0].Exclude[0] != "BOB2" || reqs[0].Reason != extension.LaunchSpawn {
		t.Fatalf("chooser asked %+v", reqs)
	}

	advisor.Account = "bob2"
	before, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "refused", Tool: "claude", Account: "bob2"}); err == nil || !strings.Contains(err.Error(), "spent until") {
		t.Fatalf("a spawn onto a spent account with no alternative was not refused: %v", err)
	}
	if after, err := h.store.ListSessions(true); err != nil || len(after) != len(before) {
		t.Fatalf("a refused spawn left rows behind: %d then %d (%v)", len(before), len(after), err)
	}
}

// list_accounts shows what the build's advisor knows of each account.
func TestListAccountsShowsTheAdvisorsState(t *testing.T) {
	reset := time.Date(2026, 11, 22, 14, 30, 0, 0, time.UTC)
	advisor := &accountstest.Advisor{Advice: extension.Advice{
		Accounts: map[string]extension.AccountState{"alice1": {Spent: true, Until: reset, Reason: "at its weekly limit", Windows: []extension.UsageWindow{
			{Name: "five_hour", Used: 12.4, ResetsAt: reset.Add(-time.Hour)},
			{Name: "seven_day", Used: 100, ResetsAt: reset},
		}}},
		Suggested: "ALICE1",
	}}
	t.Cleanup(accounts.UseChooser(advisor.Resolve))
	h := newSessionHarness(t)
	list, err := h.sessions.Accounts("claude")
	if err != nil {
		t.Fatal(err)
	}
	tool := list.Tools[0]
	state, ok := tool.States["ALICE1"]
	if !ok || !state.Spent || !state.Until.Equal(reset) || len(state.Windows) != 2 || tool.Suggested != "ALICE1" {
		t.Fatalf("list = %+v", list)
	}
	got := formatAccountsAt(list, reset.Add(-2*time.Hour))
	// The header line names the harness's accounts_command; the rest is the advisor's.
	_, got, _ = strings.Cut(got, "\n")
	want := `ALICE1  spent until 2026-11-22 14:30 UTC (in 2h0m0s): at its weekly limit; five_hour 12% resets 11-22 13:30 UTC; seven_day 100% resets 11-22 14:30 UTC  (suggested)
1 of them spent: a new session named onto a spent account is launched on another account, or refused when there is none.
`
	if got != want {
		t.Fatalf("list_accounts =\n%s\nwant\n%s", got, want)
	}
}

// Without an advisor list_accounts is the names alone, as before.
func TestListAccountsWithoutAnAdvisorIsNamesOnly(t *testing.T) {
	t.Cleanup(accounts.UseChooser((&accountstest.Chooser{}).Resolve))
	h := newSessionHarness(t)
	list, err := h.sessions.Accounts("claude")
	if err != nil {
		t.Fatal(err)
	}
	if _, got, _ := strings.Cut(FormatAccounts(list), "\n"); got != "ALICE1\n" {
		t.Fatalf("list_accounts = %q", got)
	}
}

// A pane the manager did not start is refused a routine move, which would end
// the operator's pane unasked. A move its limit already cut off takes the pane
// over instead, as the board's account card does: the pane is ended and the
// conversation comes back as a board session on the new account.
func TestBoardSwitchAccountTakesOverAnAdoptedPaneOnlyForALimitMove(t *testing.T) {
	h := newSessionHarness(t)
	socket := h.driver.SocketName()
	paneID, _ := newForeignPane(t, socket)
	adopted := store.Session{ID: uuid.NewString()[:8], Name: "own-pane", Tool: "claude", Cwd: h.caller.Cwd, Group: h.caller.Group,
		Status: status.Errored, AgentSessionID: "conv-x", TmuxSocket: socket, TmuxPaneID: paneID}
	if err := h.store.CreateSession(adopted); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.BoardSwitchAccount(adopted.ID, "alice1", false); err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("a routine move of an adopted pane was not refused: %v", err)
	}
	if !foreignPaneExists(t, socket, paneID) {
		t.Fatal("the refused move ended the operator's pane")
	}

	switched, err := h.sessions.BoardSwitchAccount(adopted.ID, "alice1", true)
	if err != nil || switched.Account != "ALICE1" {
		t.Fatalf("limit move of an adopted pane = %+v %v", switched, err)
	}
	if foreignPaneExists(t, socket, paneID) {
		t.Fatal("the adopted pane is still running after the take-over")
	}
	row, err := h.store.Get(adopted.ID)
	if err != nil || row.TmuxPaneID != "" || row.Account != "ALICE1" || row.AgentSessionID != "conv-x" {
		t.Fatalf("row after the take-over = %+v %v, want a board session on ALICE1 holding conv-x", row, err)
	}
}

// BoardLimitEpisodes hands an extension the board's decision: a session its
// limit cut off is to be moved, one showing a replayed banner is limited but
// not moved, and one since moved off the account is no longer limited.
func TestBoardLimitEpisodesSaysWhichSessionsToMove(t *testing.T) {
	chooser := &accountstest.Chooser{Account: "alice1"}
	t.Cleanup(accounts.UseChooser(chooser.Resolve))
	h := newSessionHarness(t)
	if err := h.store.SetSetting(store.AccountRoutingSetting, accounts.Extension); err != nil {
		t.Fatal(err)
	}
	if got, err := h.sessions.BoardLimitEpisodes(); err != nil || len(got) != 0 {
		t.Fatalf("no episodes: %v %v", got, err)
	}
	cut, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "cut", Tool: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	idle, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "idle", Tool: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "moved", Tool: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	record := func(id string, ep store.LimitRecovery) {
		t.Helper()
		row, err := h.store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		ep.LaunchAt = row.LaunchTime()
		if ok, err := h.store.CompareLimitRecovery(row, nil, &ep); err != nil || !ok {
			t.Fatalf("record %s: %v %v", id, ok, err)
		}
	}
	const banner = "You've hit your weekly limit · resets Friday at 9pm"
	record(cut.ID, store.LimitRecovery{Banner: banner, Account: "ALICE1", SeenAt: now})
	record(idle.ID, store.LimitRecovery{Banner: banner, Account: "ALICE1", SeenAt: now, Replayed: true})
	record(moved.ID, store.LimitRecovery{Banner: banner, Account: "BOB2", SeenAt: now})

	got, err := h.sessions.BoardLimitEpisodes()
	if err != nil {
		t.Fatal(err)
	}
	if ep, ok := got[cut.ID]; !ok || !ep.Cut || !ep.Move || ep.Account != "ALICE1" || !ep.Since.Equal(now) {
		t.Fatalf("cut session: %+v %v, want it moved", ep, ok)
	}
	if ep, ok := got[idle.ID]; !ok || ep.Cut || ep.Move {
		t.Fatalf("replayed banner: %+v %v, want limited and left alone", ep, ok)
	}
	if ep, ok := got[moved.ID]; ok {
		t.Fatalf("a session on another account than its banner's is still listed: %+v", ep)
	}
}
