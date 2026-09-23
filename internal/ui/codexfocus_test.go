package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
)

// Left leaves a focused codex pane the same way it leaves a claude one.
//
// The caret table above poses its rows by hand against cutoffs written for
// the test; this runs the shipped codex cutoff over a frame captured off the
// operator's own board, caret cell and all. Codex marks its prompt with "›"
// and fills an empty composer with placeholder text rather than leaving the
// row bare, which is the shape that has to keep reading as "nothing typed".
func TestCodexPromptHeadLeavesFocus(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	raw, err := os.ReadFile("testdata/codex-live-frame.txt")
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	m := &Model{engine: engine, mode: modeFocus}
	m.preview = string(raw)
	m.pane.forID = "s1"
	// What tmux reported over this very frame: the caret one cell past the
	// marker and its padding space, on the composer row.
	m.pane.cursor = paneCursor{x: 2, y: 63, ok: true}

	if !m.caretAtInputStart("s1", "codex") {
		t.Fatal("caret at the head of codex's prompt was not recognised, so Left cannot leave focus")
	}
	// One cell further in is inside the composer, where Left is the agent's.
	m.pane.cursor = paneCursor{x: 4, y: 63, ok: true}
	if m.caretAtInputStart("s1", "codex") {
		t.Fatal("a caret inside codex's composer read as the prompt head")
	}
}

// A dialog parks the caret on its own selection marker, which is not the head
// of a prompt -- so Left used to reach the agent and pin the operator in a
// session that was asking them something. It leaves now, unless the dialog
// says it navigates with the arrows.
func TestLeftLeavesADialogThatDoesNotUseArrows(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	cases := []struct {
		name string
		tool string
		rows []string
		// caret is the row the dialog parked the caret on: its own marker.
		caret int
		want  bool
	}{
		// Both frames are the shape the live board showed, caret on the "❯".
		{"claude permission prompt", "claude", []string{
			"  Do you want to proceed?",
			"❯ 1. Yes",
			"  2. No",
			"",
			"  Enter to confirm · Esc to cancel",
		}, 1, true},
		{"codex command approval", "codex", []string{
			"  $ echo hello",
			"› 1. Yes, proceed (y)",
			"  2. No, and tell Codex what to do differently (esc)",
			"",
			"  Press enter to confirm or esc to cancel",
		}, 1, true},
		// A legend that names the arrows generally keeps Left.
		{"claude AskUserQuestion", "claude", []string{
			"  Which approach?",
			"❯ 1. [ ] Resume the three fix rounds (Recommended)",
			"  2. [ ] Merge both in order",
			"",
			"  Enter to select · Tab/Arrow keys to navigate · Esc to cancel",
		}, 1, false},
		// The multi-question dialog the operator actually meets: Tab is what
		// steps between questions, and the "←  ☐ … →" row above the options is
		// a progress stepper, not a keybinding. Left leaves -- which is the
		// whole point, since the session is mid-question and they want to
		// glance at the board and come back to it.
		{"claude multi-question AskUserQuestion", "claude", []string{
			"  How should a parked session present on the board?",
			"←  ☐ Shape  ☐ Scope  ✔ Submit  →",
			"",
			"❯ 1. New status: delegating",
			"  2. Badge on the existing row",
			"",
			"  Enter to select · ↑/↓ to navigate · n to add notes · Tab to switch questions · Esc to cancel",
		}, 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &Model{engine: engine, mode: modeFocus}
			m.preview = strings.Join(c.rows, "\n") + "\n"
			m.pane.forID = "s1"
			m.pane.box.height, m.pane.box.width = len(c.rows), 80
			m.pane.cursor = paneCursor{x: 0, y: c.caret, ok: true}
			if m.caretAtInputStart("s1", c.tool) {
				t.Fatal("a dialog marker read as the head of a prompt")
			}
			if got := m.leftLeavesFocus("s1", c.tool); got != c.want {
				t.Fatalf("leftLeavesFocus = %v, want %v", got, c.want)
			}
		})
	}
}
