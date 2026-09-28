package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
)

func liveEngine(t *testing.T) *status.Engine {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return engine
}

// The claude half of what codex-live-frame.txt does for codex: the shipped
// cutoff run over a frame captured off a real session, caret cell and all.
// Claude pads its marker with a non-breaking space rather than an ordinary
// one, which is the byte textBeforeCaret has to treat as blank -- pose the
// row by hand and that padding is the detail that gets typed as a plain
// space and stops proving anything.
func TestClaudePromptHeadLeavesFocus(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-live-frame.txt")
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	m := &Model{engine: liveEngine(t), mode: modeFocus}
	m.preview = string(raw)
	m.pane.forID = "s1"
	// What tmux reported over this frame: the caret one cell past the marker
	// and its padding, on the composer row.
	m.pane.cursor = paneCursor{x: 2, y: 67, ok: true}

	if !m.caretAtInputStart("s1", "claude") {
		t.Fatal("caret at the head of claude's prompt was not recognised, so Left cannot leave focus")
	}
	// tmux trims a row's trailing blanks, so this composer row is the marker
	// and its padding and nothing else. A caret further along it is still
	// past nothing but blanks, which is what "nothing typed" means.
	m.pane.cursor = paneCursor{x: 9, y: 67, ok: true}
	if !m.caretAtInputStart("s1", "claude") {
		t.Fatal("an empty composer stopped reading as the prompt head once the caret moved along the trimmed row")
	}

	// With something typed, Left is the agent's line editing again.
	m.preview = "❯\u00a0draft\n"
	m.pane.cursor = paneCursor{x: 4, y: 0, ok: true}
	if m.caretAtInputStart("s1", "claude") {
		t.Fatal("a caret behind typed text read as the prompt head")
	}
}

// The Right half of the same contract: the rail sits on the right of the
// frame, so the end of the prompt is what frees the key. Posed on the live
// frame above, whose composer is empty, and on hand-posed rows for the rest.
func TestClaudePromptEndLeavesFocus(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-live-frame.txt")
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	m := &Model{engine: liveEngine(t), mode: modeFocus}
	m.preview = string(raw)
	m.pane.forID = "s1"
	m.pane.cursor = paneCursor{x: 2, y: 67, ok: true}

	if !m.caretAtInputEnd("s1", "claude") {
		t.Fatal("caret at the end of claude's empty prompt was not recognised, so Right cannot leave focus")
	}
	if !m.rightLeavesFocus("s1", "claude") {
		t.Fatal("Right does not leave claude's empty prompt")
	}

	// With something typed, Right is the agent's line editing again until
	// the end of the line.
	m.preview = "❯\u00a0draft\n"
	m.pane.cursor = paneCursor{x: 4, y: 0, ok: true}
	if m.caretAtInputEnd("s1", "claude") {
		t.Fatal("a caret inside typed text read as the prompt end")
	}
	if m.rightLeavesFocus("s1", "claude") {
		t.Fatal("Right left from inside typed text, stealing line editing from the pane")
	}
	m.pane.cursor = paneCursor{x: 7, y: 0, ok: true}
	if !m.caretAtInputEnd("s1", "claude") {
		t.Fatal("a caret at the end of typed text was not recognised")
	}
}

// The reported bug, end to end on the frame it was reported from: a
// multi-select question, the caret parked on the dialog's own marker, and
// Left refusing to return to the list.
//
// The dialog draws "←  ☐ Checks  ☐ Submit  →" above its options. Reading
// those glyphs as a claim on the horizontal arrows pinned the operator in a
// session that was mid-question -- and the glyphs claim nothing: the legend
// says the vertical pair, and Left pressed on the live dialog moves nothing.
//
// The frame lives with the status fixtures because that is the package whose
// patterns it pins; this is the same bytes read one layer up.
func TestLeftLeavesTheLiveMultiSelectDialog(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "status", "testdata", "claude-askuserquestion-multiselect.txt"))
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	m := &Model{engine: liveEngine(t), mode: modeFocus}
	m.preview = string(raw)
	m.pane.forID = "s1"
	m.pane.box.height, m.pane.box.width = 71, 319
	// Where tmux had the caret while the dialog was up: column 0 of the
	// selected option's "❯", not the head of a prompt.
	m.pane.cursor = paneCursor{x: 0, y: 40, ok: true}

	if m.caretAtInputStart("s1", "claude") {
		t.Fatal("the dialog's marker read as the head of a prompt")
	}
	if !m.leftLeavesFocus("s1", "claude") {
		t.Fatal("Left does not leave a multi-select dialog, so the operator is pinned in a session that is asking them something")
	}
}

// Left IS the question stepper. One entry in, it steps back to the previous
// question; on the first entry it does nothing -- pressed on the live dialog,
// the pane came back byte for byte identical. So the stepper's position, not
// the legend, is what says whether Left is going spare, and it overrules the
// legend: this dialog advertises "Tab/Arrow keys to navigate" on both frames
// and takes Left the same way on neither.
//
// The frames are live three-question dialogs, captured with their escapes
// because the active entry is marked by the background colour it is drawn on
// and by nothing else.
//
// "the first one answered" covers the other glyph a step is drawn in: ☒ once
// the question has been answered, which a class reading only ☐ cannot see to
// the left of the live entry.
func TestLeftLeavesOnlyTheFirstStepperEntry(t *testing.T) {
	// The legend on both captures happens to name the arrows, which on its own
	// would hold Left in either. The third case swaps in the other legend
	// claude ships -- the one a multi-select dialog draws, which claims only
	// the vertical pair -- over the same real second-step frame, so what is
	// being read is the stepper and not the wording above it.
	const arrowLegend = "Enter to select \u00b7 Tab/Arrow keys to navigate \u00b7 Esc to cancel"
	const quietLegend = "Enter to select \u00b7 \u2191/\u2193 to navigate \u00b7 n to add notes \u00b7 Tab to switch questions \u00b7 Esc to cancel"
	for _, c := range []struct {
		name   string
		file   string
		legend string
		// Each frame was recorded on its own terminal, so the pane it is
		// replayed into has to be the one it was drawn for.
		rows, cols int
		caretY     int
		want       bool
	}{
		{"first entry", "claude-stepper-first-step", arrowLegend, 71, 319, 17, true},
		{"second entry", "claude-stepper-second-step", arrowLegend, 71, 319, 17, false},
		{"second entry, legend leaving the arrows alone", "claude-stepper-second-step", quietLegend, 71, 319, 17, false},
		{"second entry, the first one answered", "claude-stepper-answered-first-step", arrowLegend, 55, 200, 19, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", c.file+".txt"))
			if err != nil {
				t.Fatalf("read frame: %v", err)
			}
			frame := strings.Replace(string(raw), arrowLegend, c.legend, 1)
			if !strings.Contains(frame, c.legend) {
				t.Fatalf("the frame no longer carries the legend this case swaps on")
			}
			m := &Model{engine: liveEngine(t), mode: modeFocus}
			m.preview = frame
			m.pane.forID = "s1"
			m.pane.box.height, m.pane.box.width = c.rows, c.cols
			// Where tmux had the caret on each: column 0 of the selected
			// option's marker, which does not move when the step does.
			m.pane.cursor = paneCursor{x: 0, y: c.caretY, ok: true}

			if got := m.leftLeavesFocus("s1", "claude"); got != c.want {
				if c.want {
					t.Fatal("Left does not leave on the first stepper entry, where the dialog has nothing to step back to")
				}
				t.Fatal("Left left the pane from a later stepper entry, so it stole the step back to the previous question")
			}
		})
	}
}

// Right is the stepper's next question, so it stays the dialog's on every
// entry but the last. The fixtures above pose the first two entries of
// three-question dialogs -- neither is last, so Right stays on both. No
// last-entry frame exists to pin the leaving half; that half reads off
// DialogStepIsLast the way the leaving half above reads off IsFirst.
func TestRightStaysOnNonLastStepperEntries(t *testing.T) {
	for _, c := range []struct {
		name string
		file string
		rows int
		cols int
		y    int
	}{
		{"first entry", "claude-stepper-first-step", 71, 319, 17},
		{"second entry", "claude-stepper-second-step", 71, 319, 17},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", c.file+".txt"))
			if err != nil {
				t.Fatalf("read frame: %v", err)
			}
			m := &Model{engine: liveEngine(t), mode: modeFocus}
			m.preview = string(raw)
			m.pane.forID = "s1"
			m.pane.box.height, m.pane.box.width = c.rows, c.cols
			m.pane.cursor = paneCursor{x: 0, y: c.y, ok: true}

			if m.rightLeavesFocus("s1", "claude") {
				t.Fatal("Right left the pane from a non-last stepper entry, so it stole the step forward to the next question")
			}
		})
	}
}
