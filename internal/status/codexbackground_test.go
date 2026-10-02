package status

import (
	"os"
	"strings"
	"testing"
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
