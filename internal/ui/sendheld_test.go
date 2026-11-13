package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Every AskUserQuestion layout holds a send, the Other-focused ones included.
// That holds whether the cursor is on the "Type something." row, a reply
// is already typed there, or the dialog shows previews or tabs. A paste into
// any of them fills the free-text row and leaves the operator unable to just
// pick an option. The panes are plain captures of real Claude dialogs, as
// tmux hands them to the hold.
func TestASendIsHeldOnEveryCapturedQuestionDialog(t *testing.T) {
	p := &poller{engine: liveEngine(t)}
	for _, file := range []string{
		"claude-2.1.284-w40-on-free-text.ansi",
		"claude-2.1.283-tabs-w80-on-free-text.ansi",
		"claude-2.1.284-w40-free-text-typed.ansi",
		"claude-2.1.283-tabs-w80-free-text-typed.ansi",
		"claude-2.1.286-w40-preview.ansi",
		"claude-2.1.286-w60-preview.ansi",
		"claude-2.1.286-w50-multiselect.ansi",
		"claude-2.1.284-w50-stepper4-first.ansi",
		"claude-2.1.284-w50-tabs-review-partial.ansi",
	} {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", file))
			if err != nil {
				t.Fatal(err)
			}
			if !p.sendHeld("claude", ansi.Strip(string(raw))) {
				t.Fatalf("a send would be pasted into the question dialog in %s", file)
			}
		})
	}
}

// A resting prompt is not held, even with an answered question still in its
// scrollback. A hold there would never lift, and the queue behind it would
// never be delivered.
func TestASendIsNotHeldAtARestingPrompt(t *testing.T) {
	p := &poller{engine: liveEngine(t)}
	pane := "⏺ Done.\n\n✻ Baked for 3s\n\n────────────────────────────────────────\n❯ \n────────────────────────────────────────\n  ? for shortcuts\n"
	if p.sendHeld("claude", pane) {
		t.Fatal("a resting prompt holds the send")
	}
	for _, file := range []string{
		"claude-2.1.284-w50-single-after.ansi",
		"claude-2.1.284-w40-single-free-after.ansi",
		"claude-2.1.284-w50-tabs-after-submit.ansi",
		"claude-2.1.286-w50-long-dc2-answered-screen.ansi",
		"claude-2.1.286-w137-long-mine-answered-screen.ansi",
	} {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", file))
			if err != nil {
				t.Fatal(err)
			}
			if p.sendHeld("claude", ansi.Strip(string(raw))) {
				t.Fatalf("an answered question in %s holds the send", file)
			}
		})
	}
}
