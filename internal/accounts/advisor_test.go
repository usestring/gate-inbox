package accounts

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/accounts/accountstest"
)

var resetAt = time.Date(2026, 11, 22, 14, 30, 0, 0, time.UTC)

func spentAdvisor(t *testing.T, pick string) *accountstest.Advisor {
	t.Helper()
	advisor := &accountstest.Advisor{
		Chooser: accountstest.Chooser{Account: pick},
		Advice: extension.Advice{
			Accounts: map[string]extension.AccountState{
				"bob2":   {Spent: true, Until: resetAt, Reason: "at its weekly limit"},
				"CAROL3": {Windows: []extension.UsageWindow{{Name: "five_hour", Used: 20}}},
			},
			Suggested: "carol3",
		},
	}
	t.Cleanup(UseChooser(advisor.Resolve))
	prev := now
	now = func() time.Time { return resetAt.Add(-2 * time.Hour) }
	t.Cleanup(func() { now = prev })
	return advisor
}

// A new session named onto an account the advisor reports spent would die
// on its first turn, so it launches on the chooser's pick instead, with the
// spent account excluded, and the caller is told why.
func TestASpawnNamedOntoASpentAccountGoesToTheChoosersPick(t *testing.T) {
	freshMarks(t)
	secrets(t, nil)
	advisor := spentAdvisor(t, "carol3")
	var warned string
	got, err := Select(testStore(t), readable, "BOB2", Request{SessionID: "s1", ToolName: "claude", Reason: extension.LaunchSpawn, Warn: func(why string) { warned = why }})
	if err != nil || got != "CAROL3" {
		t.Fatalf("Select = %q %v, want CAROL3", got, err)
	}
	want := "account BOB2 is spent until 2026-11-22 14:30 UTC (in 2h0m0s): at its weekly limit; launched on CAROL3 instead"
	if warned != want {
		t.Errorf("warning = %q\nwant      %q", warned, want)
	}
	reqs := advisor.Requests()
	if len(reqs) != 1 || !slices.Equal(reqs[0].Exclude, []string{"BOB2"}) || reqs[0].SessionID != "s1" {
		t.Fatalf("chooser asked %+v, want one request excluding BOB2", reqs)
	}
	if asked := advisor.Asked(); len(asked) != 1 || !slices.Equal(asked[0].Accounts, []string{"BOB2"}) {
		t.Fatalf("advisor asked %+v", asked)
	}
}

// With nowhere else to go the launch is refused with the account's reset,
// rather than started on an account that cannot answer.
func TestASpawnNamedOntoASpentAccountIsRefusedWhenTheChooserHasNoOther(t *testing.T) {
	freshMarks(t)
	secrets(t, nil)
	for _, tc := range []struct {
		name   string
		pick   string
		choose error
	}{
		{"the chooser names the spent account again", "BOB2", nil},
		{"the chooser picks the CLI's own login", "", nil},
		{"the chooser refuses", "", errors.New("no account has room")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			advisor := spentAdvisor(t, tc.pick)
			advisor.Err = tc.choose
			got, err := Select(testStore(t), readable, "bob2", Request{ToolName: "claude", Reason: extension.LaunchSpawn})
			var spent *SpentError
			if !errors.As(err, &spent) || got != "" {
				t.Fatalf("Select = %q %v, want a SpentError", got, err)
			}
			if !strings.Contains(err.Error(), "account BOB2 is spent until 2026-11-22 14:30 UTC") || !strings.Contains(err.Error(), "omit account") {
				t.Errorf("refusal = %q", err)
			}
		})
	}
}

// Only a spent name is redirected, and only for a new session: a revive or
// replacement keeps the account its conversation is on, and a build whose
// advisor cannot answer launches the name as given.
func TestOnlyASpawnOntoAKnownSpentAccountIsRedirected(t *testing.T) {
	freshMarks(t)
	secrets(t, nil)
	advisor := spentAdvisor(t, "carol3")
	st := testStore(t)
	for _, tc := range []struct {
		named  string
		reason extension.LaunchReason
	}{
		{"CAROL3", extension.LaunchSpawn},
		{"DAVE4", extension.LaunchSpawn},
		{"BOB2", extension.LaunchRelaunch},
		{"BOB2", extension.LaunchReplace},
	} {
		if got, err := Select(st, readable, tc.named, Request{Reason: tc.reason}); err != nil || got != tc.named {
			t.Errorf("%s on %s = %q %v, want it as named", tc.reason, tc.named, got, err)
		}
	}
	if len(advisor.Requests()) != 0 {
		t.Fatalf("the chooser was asked: %+v", advisor.Requests())
	}
	advisor.AdviceErr = errors.New("no reading yet")
	if got, err := Select(st, readable, "BOB2", Request{Reason: extension.LaunchSpawn}); err != nil || got != "BOB2" {
		t.Fatalf("with the advisor failing = %q %v, want BOB2", got, err)
	}
}

// A chooser that does not advise changes nothing about a named launch.
func TestANamedAccountWithoutAnAdvisorIsLaunchedAsNamed(t *testing.T) {
	chooser := &accountstest.Chooser{Account: "CAROL3"}
	useChooser(t, chooser)
	if got, err := Select(testStore(t), readable, "BOB2", Request{Reason: extension.LaunchSpawn}); err != nil || got != "BOB2" {
		t.Fatalf("Select = %q %v", got, err)
	}
	if _, ok := Advise(readable, "claude", []string{"BOB2"}); ok {
		t.Fatal("Advise answered with no advisor in the build")
	}
}

// Advise normalizes the names both ways, so an advisor keyed in any case
// is read.
func TestAdviseNormalizesNames(t *testing.T) {
	spentAdvisor(t, "")
	advice, ok := Advise(readable, "claude", []string{"bob2", " carol3 "})
	if !ok || !advice.Accounts["BOB2"].Spent || advice.Accounts["CAROL3"].Spent || advice.Suggested != "CAROL3" {
		t.Fatalf("Advise = %+v %v", advice, ok)
	}
}
