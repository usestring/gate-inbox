package status

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

// Delivery ends in Enter, so anything written in the composer has to hold
// it: not only text on the caret's row, which is all the gate used to read.
// A draft that wrapped or took a newline leaves the caret on a row with no
// marker, and that is how a child's report was submitted with the
// operator's half-written line in front of it.
func TestDraftInComposerSeesTheWholeComposer(t *testing.T) {
	engine, err := NewEngine(config.Config{Tools: map[string]config.Tool{
		"claude": {ActivityCutoff: `(?m)^❯`},
		"boxed":  {ActivityCutoff: `(?m)^\s*╹`, InputLine: `^[ \x{A0}]*┃`},
	}})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	cases := []struct {
		name           string
		tool           string
		caretX, caretY int
		rows           []string
		want           bool
	}{
		{"empty prompt", "claude", 2, 1, []string{"output", "❯"}, false},
		{"nbsp padded empty prompt", "claude", 2, 0, []string{"❯ "}, false},
		{"typed on the marker row", "claude", 4, 0, []string{"❯ hi"}, true},
		// A tool draws its hint after the caret; only what is before it was typed.
		{"placeholder after the caret", "claude", 2, 0, []string{"❯ Try \"fix the test\""}, false},
		// The caret sits at the start of the continuation row, so that row
		// alone is blank before it; the draft is on the marker row above.
		{"wrapped draft, caret at the wrap", "claude", 2, 1, []string{"❯ a long line that", "  wrapped"}, true},
		{"wrapped draft, caret mid-continuation", "claude", 5, 1, []string{"❯ a long line that", "  wrapped"}, true},
		// Shift-Enter: the caret on an empty new row under a written one.
		{"newline under a written line", "claude", 0, 1, []string{"❯ first line", ""}, true},
		{"boxed empty composer", "boxed", 5, 1, []string{"  ┃", "  ┃", "  ╹▀▀▀"}, false},
		{"boxed blank line under text", "boxed", 5, 2, []string{"  ┃", "  ┃  first line", "  ┃", "  ╹▀▀▀"}, true},
		{"boxed blank line under blanks", "boxed", 5, 2, []string{"  ┃", "  ┃", "  ┃", "  ╹▀▀▀"}, false},
		{"no marker above the caret", "claude", 3, 0, []string{"some output"}, false},
		// A selection dialog is indented, so its option rows read as marker
		// rows now. The option text is the tool's, not the operator's, and
		// the caret parks short of the marker: nothing before it was typed.
		{"caret parked on an indented dialog row", "claude", 1, 0, []string{" ❯ No, exit", "   Yes, I trust this folder"}, false},
		{"typed on an indented marker row", "claude", 5, 0, []string{" ❯ hi"}, true},
		// A line terminal: its prompt is history by the time the caret rests
		// on an empty row under the output, with blank rows between.
		{"caret resting under scrolled output", "claude", 0, 4, []string{"❯ the last command", "its output", "", "", ""}, false},
		{"caret resting right under wrapped output", "claude", 0, 2, []string{"❯ the last command", "its output", ""}, false},
		{"caret row past the capture", "claude", 2, 9, []string{"❯"}, false},
		{"unknown tool", "nosuch", 4, 0, []string{"❯ hi"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clean := strings.Join(c.rows, "\n")
			if got := engine.DraftInComposer(c.tool, clean, c.caretX, c.caretY); got != c.want {
				t.Fatalf("DraftInComposer = %v, want %v", got, c.want)
			}
		})
	}
}

// A marker quoted in the transcript, further up than any composer is tall,
// must not make the blank rows under it read as a draft.
func TestDraftInComposerStopsShortOfTheTranscript(t *testing.T) {
	engine, err := NewEngine(config.Config{Tools: map[string]config.Tool{
		"claude": {ActivityCutoff: `(?m)^❯`},
	}})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	rows := append([]string{"❯ the earlier prompt", "answer text"}, make([]string, composerReach+2)...)
	if engine.DraftInComposer("claude", strings.Join(rows, "\n"), 0, len(rows)-1) {
		t.Fatal("a prompt in the transcript was read as the composer")
	}
}
