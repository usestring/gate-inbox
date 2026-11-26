package status

import (
	"os"
	"strings"
	"testing"
)

// The fixtures below carry each CLI's own wording, copied from the installed
// binaries (Claude Code 2.1.326, opencode 2.3.2) and from Codex's source at
// rust-v0.169.0, laid out the way each one draws an error.

// A limit reads errored and names a banner: the banner is what the board's
// limit card and the account pool act on.
func TestCLILimitWordingIsALimit(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		tool, fixture, banner string
	}{
		{"claude", "claude-2.1.326-limit-out-of-usage-credits.txt", "⎿ You're out of usage credits. Switch to another model to continue."},
		{"claude", "claude-2.1.326-limit-reached-your.txt", "⎿ You've reached your weekly limit · resets Dec 2, 3pm (UTC)"},
		{"claude", "claude-2.1.326-limit-org-paused.txt", "⎿ Usage is paused for your organization. For help, please contact support."},
		{"claude", "claude-2.1.326-limit-model-requires-credits.txt", "⎿ Fable 5 requires usage credits. Switch models to continue."},
		// The re-armed wait line, below an earlier banner and the first wait.
		{"claude", "claude-2.1.326-limit-auto-continue-again.txt", "● Usage limit reached again · continuing automatically at 11am · esc to cancel"},
		// The footer status while Claude finishes the step on a spent
		// account: a limit even though the spinner is still running.
		{"claude", "claude-2.1.326-limit-wrapping-up.txt", "⚠ Usage limit reached · wrapping up · limit resets 9:40pm · clau.de/wrap-up"},
		// 0.169 writes the apostrophe as U+2019.
		{"codex", "codex-0.169.0-usage-limit.txt", "■ You’ve hit your usage limit. Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus), or try again at 3:00 PM."},
		{"codex", "codex-0.169.0-usage-limit-reached.txt", "■ Usage limit reached. You've reached your usage limit. Increase your limits to continue using codex."},
		{"codex", "codex-0.169.0-out-of-credits.txt", "■ You're out of credits. Your workspace is out of credits. Add credits to continue using Codex."},
		{"codex", "codex-0.169.0-spend-cap.txt", "■ You hit your spend cap set in your workspace. Increase your spend cap to continue."},
		// The member's popup replaces the composer; it is the limit, not a
		// question for anybody to answer.
		{"codex", "codex-0.169.0-workspace-credit-popup.txt", "You've reached your workspace credit limit Your workspace is out of credits. Ask your workspace owner to add more. Notify owner?"},
		{"opencode", "opencode-2.3.2-spend-limit.txt", "You've reached the spend limit for this model. Switch models to keep going."},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			pane := readFixture(t, tc.fixture)
			if got, matched := engine.Match(tc.tool, pane); got != Errored || !matched {
				t.Fatalf("Match() = (%q, %t), want (%q, true)", got, matched, Errored)
			}
			if got := engine.LimitBanner(tc.tool, pane); got != tc.banner {
				t.Fatalf("LimitBanner() = %q, want %q", got, tc.banner)
			}
		})
	}
}

// A lost login or a failure the CLI gave up retrying ends the turn errored,
// but it is not a limit: another account would not fix a transient overload,
// and a login problem is reported as one rather than as a spent account.
func TestCLIErrorWordingIsErroredNotALimit(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct{ tool, fixture string }{
		{"claude", "claude-2.1.326-auth-not-logged-in.txt"},
		{"claude", "claude-2.1.326-auth-token-invalid.txt"},
		{"claude", "claude-2.1.326-auth-token-revoked.txt"},
		{"claude", "claude-2.1.326-auth-login-changed.txt"},
		// Claude prints these only once its own retries are spent.
		{"claude", "claude-2.1.326-transient-529.txt"},
		{"claude", "claude-2.1.326-transient-temporarily-limiting.txt"},
		{"codex", "codex-0.169.0-retry-limit.txt"},
		{"codex", "codex-0.169.0-stream-disconnected.txt"},
		{"codex", "codex-0.169.0-refresh-token-expired.txt"},
		{"codex", "codex-0.169.0-context-window.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			pane := readFixture(t, tc.fixture)
			if got, matched := engine.Match(tc.tool, pane); got != Errored || !matched {
				t.Fatalf("Match() = (%q, %t), want (%q, true)", got, matched, Errored)
			}
			if got := engine.LimitBanner(tc.tool, pane); got != "" {
				t.Fatalf("LimitBanner() = %q, want none", got)
			}
		})
	}
}

// OpenCode's retry banner names the error it is retrying, which can carry a
// limit's wording; the turn is still running, so it stays working.
func TestOpencodeRetryBannerStaysWorking(t *testing.T) {
	engine := defaultEngine(t)
	pane := readFixture(t, "opencode-2.3.2-retrying.txt")
	if got, _ := engine.Match("opencode", pane); got != Working {
		t.Fatalf("Match() = %q, want %q", got, Working)
	}
	if got := engine.LimitBanner("opencode", pane); got != "" {
		t.Fatalf("LimitBanner() = %q, want none", got)
	}
}

// The same words elsewhere are not the CLI saying them: a reply bullet, a
// markdown list in a reply, a tool result followed by a reply, and a banner
// in an older turn with a newer turn below it. None may read errored, or a
// healthy session would be moved or flagged.
func TestCLIWordingQuotedIsNotErrored(t *testing.T) {
	engine := defaultEngine(t)
	rule := strings.Repeat("─", 80)
	claudeTail := "\n✻ Worked for 3s · done 5:31 PM\n\n" + rule + "\n❯ \n" + rule + "\n"
	codexTail := "\n  Worked for 3s · 17:31\n\n› Ask Codex to do anything\n"
	quoted := []string{
		"You're out of usage credits. Switch to another model to continue.",
		"You've reached your weekly limit · resets Dec 2, 3pm (UTC)",
		"Usage is paused for your organization. For help, please contact support.",
		"Fable 5 requires usage credits. Switch models to continue.",
		"Usage limit reached · wrapping up · limit resets 9:40pm",
		"Not logged in · Please run /login",
		"Please run /login · API Error: 401 OAuth access token is invalid",
		"OAuth token revoked · Please run /login",
		"API Error: Repeated 529 Overloaded errors. The API is at capacity.",
		"API Error: Server is temporarily limiting requests (not your usage limit)",
		"Automatic continue stopped · your login changed or ended · send a prompt to continue",
	}
	cases := map[string]struct{ tool, pane string }{
		// The two shapes #778's review found: a reply ending in a markdown
		// list of the menu's rows, two columns in, and a reply bullet that
		// starts with the wait line's words.
		"claude menu rows quoted as a list":  {"claude", "● The child is on its limit menu. It offers:\n\n  1. Stop and wait for limit to reset\n  2. Wait here, then continue automatically at Dec 2, 3pm\n" + claudeTail},
		"claude wait line as a reply bullet": {"claude", "● Usage limit reached · continuing automatically at 11am, so I will check back then.\n" + claudeTail},
		"codex popup title in a reply":       {"codex", "• The popup read:\n\n  You've reached your workspace credit limit\n  Usage limit reached\n" + codexTail},
		"codex limit as a reply bullet":      {"codex", "• Usage limit reached on the other workspace, so I stayed here.\n" + codexTail},
		"codex failure quoted in a list":     {"codex", "• The log shows:\n  1. ■ exceeded retry limit, last status: 429 Too Many Requests\n  2. ■ You hit your spend cap set in your workspace.\n\n• Both are from yesterday.\n" + codexTail},
		"opencode spend limit in a reply":    {"opencode", "     The docs say that once a model's budget is spent opencode shows a notice\n     about the spend limit for it.\n     Build · DeepSeek V4 Pro (New) · 4.8s · 66.7 tok/s\n  ┃\n  ╹▀▀▀▀"},
	}
	// Claude draws "● Automatic continue stopped · ..." as a system line under
	// the same marker a reply wears, so a reply that is that exact row is not
	// told apart; one that goes on past it is.
	cases["claude login-changed bullet running on"] = struct{ tool, pane string }{"claude", "● Automatic continue stopped · your login changed or ended · send a prompt to continue, it said, so I sent one.\n" + claudeTail}
	for i, line := range quoted {
		if !strings.HasPrefix(line, "Automatic continue stopped") {
			cases["claude reply bullet "+string(rune('a'+i))] = struct{ tool, pane string }{"claude", "● " + line + "\n" + claudeTail}
		}
		cases["claude reply list "+string(rune('a'+i))] = struct{ tool, pane string }{"claude", "● The pane showed:\n\n  1. " + line + "\n  2. Nothing else.\n" + claudeTail}
		cases["claude tool result "+string(rune('a'+i))] = struct{ tool, pane string }{"claude", "● Bash(cat notes.txt)\n  ⎿  " + line + "\n\n● That is an old note; nothing is wrong.\n" + claudeTail}
		cases["claude older turn "+string(rune('a'+i))] = struct{ tool, pane string }{"claude", "  ⎿  " + line + "\n\n❯ carry on\n\n● Carrying on with the review.\n" + claudeTail}
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if banner := engine.LimitBanner(tc.tool, tc.pane); banner != "" {
				t.Fatalf("LimitBanner() = %q, want none", banner)
			}
			if got, _ := engine.Match(tc.tool, tc.pane); got == Errored {
				t.Fatalf("Match() = %q, want not errored", got)
			}
		})
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
