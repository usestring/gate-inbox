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
