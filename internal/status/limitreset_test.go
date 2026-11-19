package status

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
)

func TestLimitReset(t *testing.T) {
	observed := time.Date(2026, 9, 6, 20, 30, 0, 0, time.UTC)
	for _, tc := range []struct{ name, banner, want string }{
		{"claude native", "You've hit your session limit · resets 9pm (UTC)", ""},
		{"codex year rollover", "You've hit your usage limit. Try again at Jan 2 3pm.", "2027-01-02T15:00:00Z"},
		{"codex ordinal", "You've hit your usage limit. Upgrade to Plus, or try again at Sep 7th, 2026 10:42 AM.", "2026-09-07T10:42:00Z"},
		{"codex dated", "You've hit your usage limit. Try again at Sep 7, 2026, 10:42 AM.", "2026-09-07T10:42:00Z"},
		{"codex clock", "You've hit your usage limit. Try again at 4:44 PM.", "2026-09-07T16:44:00Z"},
		{"codex expired date", "You've hit your usage limit. Try again at Sep 5th, 2026 10:42 AM.", "2026-09-05T10:42:00Z"},
		{"codex 24 hour", "You've hit your usage limit. Try again at 7 Sep 2026, 08:25.", "2026-09-07T08:25:00Z"},
		{"unknown time", "You've hit your usage limit. Try again later.", ""},
		{"no limit", "The tests reset at 9pm", ""},
		{"invalid clock", "You've hit your usage limit. Try again at 25pm.", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, ok := LimitReset(tc.banner, observed)
			if tc.want == "" {
				if ok {
					t.Fatalf("unexpected reset: %v", at)
				}
				return
			}
			if !ok || at.UTC().Format(time.RFC3339) != tc.want {
				t.Fatalf("got %v (%v), want %s", at, ok, tc.want)
			}
		})
	}
}

func TestLimitResetCurrentMinuteAndDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 3, 7, 23, 30, 0, 0, loc)
	at, ok := LimitReset("You've hit your usage limit. Try again at 4:00 AM.", observed)
	want := time.Date(2026, 3, 8, 4, 0, 0, 0, loc)
	if !ok || !at.Equal(want) {
		t.Fatalf("got %v, want %v", at, want)
	}
	observed = time.Date(2026, 9, 6, 21, 0, 30, 0, time.UTC)
	at, ok = LimitReset("You've hit your usage limit. Try again at 9pm.", observed)
	if !ok || !at.Equal(observed.Truncate(time.Minute)) {
		t.Fatalf("current minute rolled forward: %v", at)
	}
}

func TestLimitBannerUsesOnlyCurrentVisibleLimit(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	banner := "You've hit your weekly limit · resets 1am (Asia/Jerusalem)"
	for _, tc := range []struct {
		name, pane string
		want       bool
	}{
		{"current", "  ⎿  " + banner + "\n\n✻ Cooked for 2s\n\n❯ ", true},
		{"old", "  ⎿  " + banner + "\n\n✻ Cooked for 2s\nNew answer\n✻ Cooked for 3s\n❯ ", false},
		{"typed", "New answer\n❯ " + banner, false},
		{"scrolled", "  ⎿  " + banner + "\nJump to bottom (ctrl+End)\n❯ ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := e.LimitBanner("claude", tc.pane)
			if (got != "") != tc.want {
				t.Fatalf("banner = %q", got)
			}
		})
	}
	wrapped := "■ You've hit your usage limit. Upgrade to Plus to continue using Codex,\n  or try again at Sep 7th, 2026 10:42 AM.\n\n› Ask Codex to do anything"
	got := e.LimitBanner("codex", wrapped)
	if _, ok := LimitReset(got, time.Now()); !ok {
		t.Fatalf("wrapped Codex banner lost reset: %q", got)
	}
}

// Claude's limit menu leaves no input line on the screen, so the banner is
// found without one. It is still the banner's own row, not the reply, lists
// and table above it, which the limit card would print as one paragraph.
func TestLimitBannerUnderTheLimitMenuIsOnlyTheBanner(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	banner := "You've hit your weekly limit · resets Nov 7, 11am (UTC)"
	pane := "  1. Rollout:\n" +
		"     - Raise the size limits before the first run.\n" +
		"     - Put them back after two clean runs.\n" +
		"\n" +
		"  ┌──────────┬──────────┐\n" +
		"  │ Step     │ The tool │\n" +
		"  ├──────────┼──────────┤\n" +
		"  │ Edit     │ One line │\n" +
		"  └──────────┴──────────┘\n" +
		"\n" +
		"● User answered Claude's questions:\n" +
		"  ⎿  · Add it to the PR? → Add it (Recommended)\n" +
		"  ⎿  " + banner + "\n" +
		"\n" +
		"✻ Crunched for 4m 12s · done 5:17 PM\n" +
		"▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔\n" +
		"   What do you want to do?\n" +
		"\n" +
		"   ❯ 1. Stop and wait for limit to reset\n" +
		"     2. Wait here, then continue automatically at Nov 7, 11am\n" +
		"     3. Switch to usage credits\n" +
		"\n" +
		"   Enter to confirm · Esc to cancel\n"
	if got := e.LimitBanner("claude", pane); got != "⎿ "+banner {
		t.Fatalf("banner = %q, want only the banner's row", got)
	}
}

// The limited turn's own "❯" prompt echo above the limit menu is not an
// input line, so the banner below it still counts. A real input line below
// the banner still bounds the turn, and a banner typed on the input line is
// still a draft.
func TestLimitMenuBelowTheLimitedTurnsPromptIsErrored(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	banner := "You've hit your weekly limit · resets Nov 21, 12pm (UTC)"
	menu := "▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔\n" +
		"  What do you want to do?\n" +
		"\n" +
		"  ❯ 1. Stop and wait for limit to reset\n" +
		"    2. Wait here, then continue automatically at Nov 21, 12pm\n" +
		"    3. Add funds to continue with usage credits\n" +
		"\n" +
		"  Enter to confirm · Esc to cancel\n"
	inputLine := "\n✻ Churned for 2s · done 12:10 AM\n" +
		"──────────────────────────────\n" +
		"❯ \n" +
		"──────────────────────────────\n" +
		"  repo | main | Opus | ctx 40k/200k\n"
	for _, tc := range []struct {
		name, pane, status string
	}{
		{"keep-alive prompt echoed above the banner",
			"✻ Churned for 5s · done 12:09 AM\n" +
				"\n" +
				"❯ [gate-inbox] Prompt-cache keep-alive, not a task: reply only ok, no tools.\n" +
				"  ⎿  " + banner + "\n" +
				menu, Errored},
		{"message echoed above the banner",
			"✻ Cogitated for 31s · done 1:29 PM\n" +
				"\n" +
				"❯ [gate-inbox] From an extension running on this board.\n" +
				"  Your child session moved to another account. Nothing for you to do.\n" +
				"  ⎿  " + banner + "\n" +
				menu, Errored},
		{"note below the turn-end summary",
			"❯ carry on\n" +
				"  ⎿  " + banner + "\n" +
				"\n" +
				"✻ Crunched for 1s · done Thursday 3:49 PM\n" +
				"\n" +
				"● gate-inbox: plugin.json changed — reloaded\n" +
				menu, Errored},
		{"input line below the banner",
			"❯ carry on\n" +
				"  ⎿  " + banner + "\n" +
				inputLine, Errored},
		{"newer turn below the banner",
			"❯ carry on\n" +
				"  ⎿  " + banner + "\n" +
				"\n" +
				"✻ Crunched for 1s · done 3:49 PM\n" +
				"\n" +
				"❯ next task\n" +
				"● Working on it.\n" +
				inputLine, Finished},
		{"banner typed on the input line",
			"● Done.\n" +
				"\n" +
				"✻ Crunched for 1s · done 3:49 PM\n" +
				"──────────────────────────────\n" +
				"❯ " + banner + "\n" +
				"──────────────────────────────\n", Finished},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := e.Match("claude", tc.pane); got != tc.status {
				t.Fatalf("status = %q, want %q", got, tc.status)
			}
			if tc.status != Errored {
				return
			}
			if got := e.LimitBanner("claude", tc.pane); got != "⎿ "+banner {
				t.Fatalf("banner = %q, want the banner's row", got)
			}
		})
	}
}
