package status

import (
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

func codexEngine(t *testing.T) *Engine {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return engine
}

func codexFrame(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/codex-0153-settled-turn.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(raw)
}

// Codex 0.153 closes a turn with a plain full-width rule. The configured
// turn_end still looks for the "─ Worked for 12s ─" summary older builds
// drew, so it matches nothing: scope is never narrowed and no turn ever
// resolves through turnState.
func TestCodexTurnEndMatchesARealFrame(t *testing.T) {
	tr := codexEngine(t).tools["codex"]
	if tr.turnEnd == nil {
		t.Fatal("codex has no turn_end configured")
	}
	frame := codexFrame(t)
	if !tr.turnEnd.MatchString(frame) {
		t.Fatalf("turn_end %q matches nothing in a real settled codex frame", tr.turnEnd)
	}
}

// A turn that ended is scoped away, so an error it printed cannot keep
// deciding the status of every later turn.
func TestCodexStaleErrorDoesNotOutliveItsTurn(t *testing.T) {
	engine := codexEngine(t)
	frame := codexFrame(t)
	// Put a codex error banner into the FIRST turn, above the rule that
	// closes it. The newest turn below is an ordinary settled exchange.
	withError := strings.Replace(frame,
		"  └ zsh:1: command not found: nonexistent-binary-xyz",
		"  └ zsh:1: command not found: nonexistent-binary-xyz\n■ stream error: unexpected status 500; retrying",
		1)
	if withError == frame {
		t.Fatal("fixture did not take the injected error line")
	}
	if got, _ := engine.Match("codex", withError); got == Errored {
		t.Fatal("an error from a finished turn still reads as the session's status")
	}
}

func TestCodexAnimatedComposerDoesNotChangeActivity(t *testing.T) {
	engine := codexEngine(t)
	for _, content := range []string{"• Done.\n", "• Continue?\n", "• Working (12s • esc to interrupt)\n"} {
		for _, animation := range []string{"", "⠁   ⠈    ⢀  ⡀\n", "  ⠄ ⠠    ⠂\n\n"} {
			pane := content + "\n" + animation + "› Ask Codex to do anything⡀\n ⢀  ⠂\n"
			region, ok := engine.ActivityRegion("codex", pane)
			if !ok || strings.TrimSpace(region) != strings.TrimSpace(content) {
				t.Fatalf("activity region = %q, ok=%v; want %q", region, ok, content)
			}
			if strings.Contains(content, "esc to interrupt") {
				if got, matched := engine.Match("codex", pane); got != Working || !matched {
					t.Fatalf("active spinner = %q, matched=%v", got, matched)
				}
			}
			if prefix, ok := engine.InputPrefix("codex", "› Ask Codex to do anything⡀"); !ok || prefix != "›" {
				t.Fatalf("input prefix = %q", prefix)
			}
		}
	}
}

func TestCodexMCPStartupWarningsDoNotHideCompletedTurn(t *testing.T) {
	engine := codexEngine(t)
	warnings := "⚠ The string-web-access MCP server is not logged in. Run `codex mcp login string-web-access`.\n\n⚠ MCP startup incomplete (failed: string-web-access)\n"
	for _, tc := range []struct{ answer, want string }{{"• Done.", Finished}, {"• Continue?", Waiting}} {
		content := tc.answer + "\n\n─ Worked for 1m 02s ────────────────\n\n" + warnings
		pane := content + "\n⠁   ⠈    ⢀  ⡀\n› Ask Codex to do anything\n"
		if got, matched := engine.Match("codex", pane); got != tc.want || !matched {
			t.Fatalf("completed turn = %q, matched=%v; want %q", got, matched, tc.want)
		}
		if got := engine.TurnEndedState("codex", tc.answer+"\n"+warnings); got != tc.want {
			t.Fatalf("markerless turn = %q; want %q", got, tc.want)
		}
		streaming := strings.Replace(pane, warnings, warnings+"• New output\n", 1)
		if got, matched := engine.Match("codex", streaming); matched && (got == Finished || got == Waiting) {
			t.Fatalf("new output beneath warnings still reads as completed: %q", got)
		}
	}
}
