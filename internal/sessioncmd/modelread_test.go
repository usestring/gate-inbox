package sessioncmd

import (
	"strings"
	"testing"
)

const claudePane = `● Read 3 files

● The review is done; the blocking item is finding 1.

✻ Churned for 3m 30s · done 12:32 AM

────────────────────────────────────────
❯ half-typed draft
────────────────────────────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`

func TestStripClaudeChrome(t *testing.T) {
	got := stripClaudeChrome(claudePane)
	want := "● Read 3 files\n\n● The review is done; the blocking item is finding 1.\n❯ half-typed draft"
	if got != want {
		t.Fatalf("stripped = %q, want %q", got, want)
	}
	// A notice under the box is the session's news, not its frame.
	limited := "● Working on it.\n\n────────\n❯ \n────────\n  ⏵⏵ bypass permissions on (shift+tab to cycle)\n  ⚠ Usage limit reached · limit resets 2am\n  Continuing automatically at 2am · Esc to cancel"
	if got := stripClaudeChrome(limited); got != "● Working on it.\n  ⚠ Usage limit reached · limit resets 2am\n  Continuing automatically at 2am · Esc to cancel" {
		t.Fatalf("stripped = %q; want the usage-limit notice kept", got)
	}
	plain := "no input box here\n───\njust a rule"
	if got := stripClaudeChrome(plain); got != plain {
		t.Fatalf("a pane with no input box changed: %q", got)
	}
}

func TestForModel(t *testing.T) {
	result := "The review is done; the blocking item is finding 1."
	screen := SessionScreen{
		Session: Session{ID: "abc", Name: "review", Tool: "claude", Status: "finished", Directory: "/somewhere/long", Running: true},
		Output:  claudePane,
		Mode:    "pane",
		Cursor:  "c@1",
		Digest:  ReadDigest{Status: "finished", Result: result},
	}
	got := screen.ForModel()
	if got.Session != (SessionRef{ID: "abc", Name: "review", Tool: "claude", Status: "finished"}) {
		t.Fatalf("session = %#v", got.Session)
	}
	if strings.Contains(got.Output, result) || !strings.Contains(got.Output, "● "+resultMarker) {
		t.Fatalf("result was not replaced by the marker: %q", got.Output)
	}
	if strings.Contains(got.Output, "bypass permissions") || strings.Contains(got.Output, "────") || !strings.Contains(got.Output, "half-typed") {
		t.Fatalf("chrome kept or draft lost: %q", got.Output)
	}
	if got.Digest.Result != result || got.Cursor != "c@1" || got.Mode != "pane" {
		t.Fatalf("digest, cursor or mode changed: %#v", got)
	}

	// A delta is the transcript, not a pane: no chrome to cut, and other
	// tools' panes are left whole.
	delta := screen
	delta.Mode, delta.Output = "delta", "> go\n\n"+result
	if got := delta.ForModel().Output; got != "> go\n\n"+resultMarker {
		t.Fatalf("delta output = %q", got)
	}
	codex := screen
	codex.Session.Tool = "codex"
	if got := codex.ForModel().Output; !strings.Contains(got, "bypass permissions") {
		t.Fatalf("a codex pane lost lines: %q", got)
	}

	short := screen
	short.Digest.Result = "done"
	short.Output = "done"
	if got := short.ForModel().Output; got != "done" {
		t.Fatalf("a short result was elided: %q", got)
	}
}
