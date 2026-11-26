package status

import (
	"os"
	"strings"
	"testing"
)

// Claude's usage-limit menu is a limit, not a dialog. Reading it as waiting
// relays it to the parent as a question for the user, and the account pool
// moves a limited session only once it reads errored, so the session sits on
// the menu instead of rotating to another account.
func TestClaudeLimitMenuIsErrored(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		fixture string
		banner  string
	}{
		// The pool's carry-on message, typed into a session it moved onto an
		// account that was already spent. Claude opened the menu on submit
		// and drew no banner for the turn; the one on screen is the previous
		// account's, above the newer prompt. (Board relay of 2026-11-25.)
		{"claude-2.1.326-limit-menu-after-move.txt", "2. Wait here, then continue automatically at Nov 28, 12pm"},
		// The limit hit mid-turn: banner, then the menu Claude opens itself
		// with /rate-limit-options. The banner still leads.
		{"claude-2.1.326-limit-menu-rate-limit-options.txt", "⎿ You've hit your weekly limit · resets Dec 2, 3pm (UTC)"},
		{"claude-2.1.326-limit-menu-upgrade.txt", "⎿ You've hit your session limit · resets 11am (UTC)"},
		// "Wait here, then continue automatically" was chosen: the session is
		// parked until the reset, which another account would not need.
		{"claude-2.1.326-limit-auto-continue-wait.txt", "● Usage limit reached · continuing automatically at 11am · esc to cancel"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			raw, err := os.ReadFile("testdata/" + tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			pane := string(raw)
			if got, matched := engine.Match("claude", pane); got != Errored || !matched {
				t.Fatalf("Match() = (%q, %t), want (%q, true)", got, matched, Errored)
			}
			if got := engine.LimitBanner("claude", pane); got != tc.banner {
				t.Fatalf("LimitBanner() = %q, want %q", got, tc.banner)
			}
		})
	}
}

// The menu stays a dialog for typing purposes: text typed into it would
// pick an option rather than reach the agent.
func TestClaudeLimitMenuStillHoldsTyping(t *testing.T) {
	engine := defaultEngine(t)
	raw, err := os.ReadFile("testdata/claude-2.1.326-limit-menu-after-move.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.TypingHold("claude", string(raw)); got != Waiting {
		t.Fatalf("TypingHold() = %q, want %q", got, Waiting)
	}
}

// The menu's wording elsewhere on screen is not the menu: a tool result
// printing it sits under its own marker five columns in, prose quotes it
// mid-line, and a menu from an older turn has a newer turn below it.
func TestClaudeLimitMenuQuotedIsNotALimit(t *testing.T) {
	engine := defaultEngine(t)
	rule := strings.Repeat("─", 80)
	cases := map[string]string{
		"tool result": "● Bash(cat testdata/limit-menu.txt)\n" +
			"  ⎿     What do you want to do?\n" +
			"\n" +
			"       ❯ 1. Stop and wait for limit to reset\n" +
			"         2. Wait here, then continue automatically at Dec 2, 3pm\n" +
			"\n" +
			"● The fixture reads as the menu.\n" +
			"\n" +
			"✻ Worked for 12s · done 5:30 PM\n" +
			"\n" + rule + "\n❯ \n" + rule + "\n",
		"prose": "● The child's pane reads \"What do you want to do?\", with \"Stop and wait for limit to reset\" or\n" +
			"  \"Wait here, then continue automatically at Dec 2, 3pm\".\n" +
			"\n" +
			"✻ Worked for 3s · done 5:31 PM\n" +
			"\n" + rule + "\n❯ \n" + rule + "\n",
		"older turn": "● Usage limit reached · continuing automatically at 11am · esc to cancel\n" +
			"\n" +
			"❯ carry on\n" +
			"\n" +
			"● Carrying on with the review.\n" +
			"\n" +
			"✻ Worked for 40s · done 11:02 AM\n" +
			"\n" + rule + "\n❯ \n" + rule + "\n",
	}
	for name, pane := range cases {
		t.Run(name, func(t *testing.T) {
			if banner := engine.LimitBanner("claude", pane); banner != "" {
				t.Fatalf("LimitBanner() = %q, want none", banner)
			}
			if got, _ := engine.Match("claude", pane); got == Errored {
				t.Fatalf("Match() = %q, want not errored", got)
			}
		})
	}
}
