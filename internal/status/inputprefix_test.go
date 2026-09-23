// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

func TestInputPrefix(t *testing.T) {
	engine, err := NewEngine(config.Config{Tools: map[string]config.Tool{
		"claude":     {ActivityCutoff: `(?m)^❯`},
		"gemini":     {ActivityCutoff: `(?m)^\s*[>!*] `},
		"unmarked":   {},
		"degenerate": {ActivityCutoff: `(?m)^`},
	}})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	cases := []struct {
		name   string
		tool   string
		row    string
		prefix string
		ok     bool
	}{
		{"bare marker", "claude", "❯", "❯", true},
		{"marker with input", "claude", "❯ write a test", "❯", true},
		// The marker has to be the row's first written thing, not its
		// first cell: Claude indents its trust and permission dialogs.
		{"indented marker", "claude", " ❯ No, exit", " ❯", true},
		{"deeply indented marker", "claude", "    ❯ 1. Yes", "    ❯", true},
		// Quoted mid-line it is content, which is what the rule is for.
		{"quoted marker", "claude", "we use ❯ here", "", false},
		{"content row", "claude", "some output", "", false},
		// gemini's composer marker carries its own indent and trailing space.
		{"marker owning its indent", "gemini", "  > hi", "  > ", true},
		{"shell mode marker", "gemini", "  ! ls", "  ! ", true},
		{"tool without a cutoff", "unmarked", "❯", "", false},
		{"unknown tool", "nosuch", "❯", "", false},
		// A cutoff that matches zero width marks no prompt: it would
		// otherwise stamp every row as one.
		{"zero-width cutoff", "degenerate", "any row at all", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefix, ok := engine.InputPrefix(c.tool, c.row)
			if prefix != c.prefix || ok != c.ok {
				t.Fatalf("InputPrefix = (%q, %v), want (%q, %v)", prefix, ok, c.prefix, c.ok)
			}
		})
	}
}

// opencode boxes its composer, so the divider its activity_cutoff finds is
// drawn a row BELOW the one the caret types on and cannot name the input
// line. Every composer row went unrecognised, which is what left the board's
// Left key with no prompt head to leave from in an opencode session; the
// shipped config names the box's bar separately. Rows are verbatim from a
// live pane (session gi_fa29e1fd, 2026-09-08).
func TestInputPrefixReadsOpencodeComposer(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	cases := []struct {
		name string
		row  string
		want bool
	}{
		{"empty composer row", "  ┃", true},
		{"composer row with a message", "  ┃  do a full check", true},
		// input_line replaces the cutoff rather than joining it, so the
		// divider stops counting as an input line: nothing is typed on it.
		{"the box's closing divider", "  ╹▀▀▀", false},
		{"the model line under the box", "   /home/user/repos/sample-repo", false},
		{"a transcript row", "     │ec2-52-9-85-115.us-west-1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := engine.InputPrefix("opencode", c.row); ok != c.want {
				t.Fatalf("InputPrefix(%q) ok = %v, want %v", c.row, ok, c.want)
			}
		})
	}
}

// TestInputPrefixReadsAnIndentedDialogMarker drives the shipped config over a
// real Claude trust prompt (testdata/claude-trust-prompt.txt). Its marker row
// is indented by one space, which a column-0 anchor read as content -- so
// selectionDialogUp saw no dialog, answersFocused admitted neither gesture,
// and a triage queue stopped on a session that had already been answered.
func TestInputPrefixReadsAnIndentedDialogMarker(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	pane, err := os.ReadFile(filepath.Join("testdata", "claude-trust-prompt.txt"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	// The dialog must still read as waiting, or the row would not have looked
	// stuck in the first place and this fix would be answering the wrong bug.
	if state, matched := engine.RuleMatch("claude", string(pane)); !matched || state != Waiting {
		t.Fatalf("RuleMatch = (%v, %v), want (%v, true)", state, matched, Waiting)
	}
	var markerRows []string
	for _, row := range strings.Split(string(pane), "\n") {
		if prefix, ok := engine.InputPrefix("claude", row); ok {
			markerRows = append(markerRows, prefix)
		}
	}
	if len(markerRows) != 1 || markerRows[0] != " \u276f" {
		t.Fatalf("marker rows = %q, want exactly [\" \u276f\"]", markerRows)
	}
}
