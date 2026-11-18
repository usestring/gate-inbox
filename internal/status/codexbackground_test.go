package status

import (
	"os"
	"strings"
	"testing"
	"time"
)

// 2026-10-02 real capture, codex 0.157: a turn that left a command running
// in a background terminal closes on its time label with the terminal named
// on a row under it. Ending the session closes the terminal, so the turn is
// working, not finished.
func TestCodexBackgroundTerminalKeepsTheTurnWorking(t *testing.T) {
	raw, err := os.ReadFile("testdata/codex-0157-background-terminal.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	engine := codexEngine(t)
	if got, _ := engine.Match("codex", string(raw)); got != Working {
		t.Fatalf("Match = %q, want working while a background terminal runs", got)
	}
	// The same frame once the terminal has gone settles as the turn it was.
	drained := strings.Replace(string(raw), "  1 background terminal running · /ps to view · /stop to close\n", "", 1)
	if got, _ := engine.Match("codex", drained); got == Working {
		t.Fatalf("Match = %q with no background terminal left", got)
	}
}

func TestCodexShellOnlyTurnSettlesAsBackground(t *testing.T) {
	raw, err := os.ReadFile("testdata/codex-0157-background-terminal.txt")
	if err != nil {
		t.Fatal(err)
	}
	pane := string(raw)
	engine := codexEngine(t)
	for _, tc := range []struct {
		name, pane string
		background bool
	}{
		{"shell only", pane, true},
		{"multiple shells", strings.ReplaceAll(pane, "1 background terminal", "2 background terminals"), true},
		{"active turn", strings.Replace(pane, "› Ask Codex", "• Working (12s • esc to interrupt)\n\n› Ask Codex", 1), false},
		{"permission prompt", strings.Replace(pane, "› Ask Codex to do anything", "› 1. Yes, proceed\n  2. No, and tell Codex what to do differently\n\n  Press enter to confirm or esc to cancel", 1), false},
		{"new output", strings.Replace(pane, "› Ask Codex", "• A new response is streaming\n› Ask Codex", 1), false},
		{"drained", strings.Replace(pane, "  1 background terminal running · /ps to view · /stop to close\n", "", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := SettleBackground(engine, "codex", tc.pane, func() (time.Duration, bool) {
				t.Fatal("Codex must not read a Claude transcript age")
				return 0, false
			})
			if ok != tc.background || ok && got != Background {
				t.Fatalf("SettleBackground = %q, %v; background = %v", got, ok, tc.background)
			}
		})
	}
}
