package status

import (
	"os"
	"strings"
	"testing"
)

// 2026-10-12 live capture, trimmed: the turn ended at 15:14 six days before,
// and the claude process had no child shell left, yet the tail still said
// "1 shell still running" and the row read working all that time.
func TestBackgroundRestReadsAStaleShellTail(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-stale-shell-tail.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	engine := defaultEngine(t)
	if got, _ := engine.Match("claude", string(raw)); got != Working {
		t.Fatalf("Match = %q, want working while the tail stands", got)
	}
	got, ok := engine.BackgroundRest("claude", string(raw))
	if !ok || got != Finished {
		t.Fatalf("BackgroundRest = %q, %v; want finished, true", got, ok)
	}
}

// Background agents spend tokens while they run, so their wait line is not a
// tail to settle, however long it stands.
func TestBackgroundRestLeavesBackgroundAgentsWorking(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-stale-shell-tail.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	pane := strings.Replace(string(raw), "✻ Baked for 26s · done 15:14 · 1 shell still running", "✻ Waiting for 2 background agents to finish", 1)
	engine := defaultEngine(t)
	if got, _ := engine.Match("claude", pane); got != Working {
		t.Fatalf("Match = %q, want working", got)
	}
	if got, ok := engine.BackgroundRest("claude", pane); ok {
		t.Fatalf("BackgroundRest = %q, true for background agents", got)
	}
	drained := strings.Replace(string(raw), " · 1 shell still running", "", 1)
	if _, ok := engine.BackgroundRest("claude", drained); ok {
		t.Fatal("BackgroundRest claimed a turn with nothing left running")
	}
}

// 2026-10-13 live capture, trimmed: once the context is large Claude
// right-aligns a "/clear" hint under the turn-end summary. Read as content
// below the summary, it hid the turn end, so the shell tail never settled and
// a child that had stopped read working until the operator typed into it.
func TestBackgroundRestReadsAShellTailUnderAClearHint(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-shell-tail-clear-hint.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	engine := defaultEngine(t)
	if got, _ := engine.Match("claude", string(raw)); got != Working {
		t.Fatalf("Match = %q, want working while the tail stands", got)
	}
	got, ok := engine.BackgroundRest("claude", string(raw))
	if !ok || got != Finished {
		t.Fatalf("BackgroundRest = %q, %v; want finished, true", got, ok)
	}
}
