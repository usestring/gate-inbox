package dialog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The fixtures under testdata/claude-2.1.283-tabs-* are real captures of one
// four-question and one three-question AskUserQuestion, drawn by Claude Code
// 2.1.283 on a private tmux server at the widths their names give, and cut to
// the dialog. The .ansi ones keep the escapes, which is the only place the
// tab being shown is recorded. claude-2.1.283-tabs-reported-narrow.txt is the
// pane a parent was refused on, rebuilt from its report.

func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(raw)
}

func TestTabbedDialogsParseAtEveryWidth(t *testing.T) {
	for _, tc := range []struct {
		file     string
		prompt   string
		steps    int
		answered int
		freeText int
	}{
		{"claude-2.1.283-tabs-w160-first.ansi",
			"Which package manager should the new workspace standardise on for every JavaScript package?",
			4, 0, 4},
		{"claude-2.1.283-tabs-w80-second.ansi",
			"Should the release pipeline block on the licence compliance scan?",
			4, 0, 3},
		{"claude-2.1.283-tabs-w50-first.ansi",
			"Which package manager should the new workspace\nstandardise on for every JavaScript package?",
			4, 0, 4},
		{"claude-2.1.283-tabs-w40-first.ansi",
			"Which package manager should the new\nworkspace standardise on for every\nJavaScript package?",
			4, 0, 4},
		{"claude-2.1.283-tabs-w50-second.ansi",
			"Should the release pipeline block on the licence\ncompliance scan?", 4, 0, 3},
		{"claude-2.1.283-tabs-w44-first.ansi", "Which build tool?", 3, 0, 3},
		{"claude-2.1.283-tabs-w44-third.ansi", "How often do we cut a release?", 3, 0, 3},
		{"claude-2.1.283-tabs-w80-after-first-answer.ansi",
			"Should the release pipeline block on the licence compliance scan?", 4, 1, 3},
		{"claude-2.1.283-tabs-w80-on-free-text.ansi",
			"Should the release pipeline block on the licence compliance scan?", 4, 1, 3},
		{"claude-2.1.283-tabs-reported-narrow.txt",
			"Should the release pipeline block on the\nlicence compliance scan?", 4, 0, 3},
	} {
		t.Run(tc.file, func(t *testing.T) {
			got, ok := Parse(ansi.Strip(fixture(t, tc.file)))
			if !ok {
				t.Fatal("a tabbed AskUserQuestion did not parse")
			}
			if got.Kind != KindAsk {
				t.Errorf("kind = %q, want AskUserQuestion", got.Kind)
			}
			if got.Prompt != tc.prompt {
				t.Errorf("prompt = %q, want %q", got.Prompt, tc.prompt)
			}
			if got.Steps != tc.steps || got.Answered != tc.answered {
				t.Errorf("steps = %d answered = %d, want %d and %d", got.Steps, got.Answered, tc.steps, tc.answered)
			}
			if got.FreeText != tc.freeText {
				t.Errorf("free-text row = %d, want %d", got.FreeText, tc.freeText)
			}
			if got.Cursor == 0 {
				t.Error("cursor not located")
			}
			if got.Refusal() != "" {
				t.Errorf("refused: %s", got.Refusal())
			}
		})
	}
}

// The descriptions under each choice are indented prose, not numbered rows,
// so the choices are the labels alone. Checked once in full here rather than
// per width above.
func TestTabbedDialogChoicesAreTheLabels(t *testing.T) {
	got, ok := Parse(ansi.Strip(fixture(t, "claude-2.1.283-tabs-w50-first.ansi")))
	if !ok {
		t.Fatal("did not parse")
	}
	want := []string{"Bun", "pnpm", "npm", "Type something.", "Chat about this"}
	if !reflect.DeepEqual(got.Options, want) {
		t.Errorf("options = %q, want %q", got.Options, want)
	}
	if !reflect.DeepEqual(got.Choices(10), []string{"Bun", "pnpm", "npm"}) {
		t.Errorf("choices = %q", got.Choices(10))
	}
}

// Revisiting an answered question draws a tick after the choice made. The
// tick is not part of the choice, or "pnpm" would stop matching an answer of
// "pnpm".
func TestARevisitedQuestionNamesItsPick(t *testing.T) {
	got, ok := Parse(ansi.Strip(fixture(t, "claude-2.1.283-tabs-w80-revisit-answered.ansi")))
	if !ok {
		t.Fatal("did not parse")
	}
	if got.Picked != 2 || got.Options[1] != "pnpm" {
		t.Errorf("picked = %d, options = %q; want 2 and a bare pnpm", got.Picked, got.Options)
	}
	if got.Choose("pnpm") != 2 {
		t.Errorf("Choose(pnpm) = %d, want 2", got.Choose("pnpm"))
	}
}

// Words typed into the free-text row replace its label, and the legend grows
// a segment and wraps at 80 columns.
func TestTheFreeTextRowWhileTyping(t *testing.T) {
	got, ok := Parse(ansi.Strip(fixture(t, "claude-2.1.283-tabs-w80-free-text-typed.ansi")))
	if !ok {
		t.Fatal("did not parse with words typed into the free-text row")
	}
	if got.Cursor != 3 || got.Options[2] != "Warn for a month, then block" {
		t.Errorf("cursor = %d, options = %q", got.Cursor, got.Options)
	}
}

func TestAMultiSelectTabIsStillRefused(t *testing.T) {
	got, ok := Parse(ansi.Strip(fixture(t, "claude-2.1.283-tabs-w44-multiselect.ansi")))
	if !ok {
		t.Fatal("did not parse")
	}
	if !got.MultiSelect || got.Refusal() == "" {
		t.Errorf("multi-select = %v, refusal = %q", got.MultiSelect, got.Refusal())
	}
	if got.Steps != 3 {
		t.Errorf("steps = %d, want 3", got.Steps)
	}
}

func TestParseStepperReadsTabsAndTheActiveOne(t *testing.T) {
	for _, tc := range []struct {
		file     string
		labels   []string
		answered []bool
		active   int
	}{
		{"claude-2.1.283-tabs-w160-first.ansi",
			[]string{"Tooling", "Compliance", "npm publish", "Run tests"}, []bool{false, false, false, false}, 0},
		{"claude-2.1.283-tabs-w50-first.ansi",
			[]string{"Tooling", "Com…", "npm…", "Run…"}, []bool{false, false, false, false}, 0},
		{"claude-2.1.283-tabs-w80-second.ansi",
			[]string{"Tooling", "Compliance", "npm publish", "Run tests"}, []bool{false, false, false, false}, 1},
		{"claude-2.1.283-tabs-w80-after-first-answer.ansi",
			[]string{"Tooling", "Compliance", "npm publish", "Run tests"}, []bool{true, false, false, false}, 1},
		{"claude-2.1.283-tabs-w80-revisit-answered.ansi",
			[]string{"Tooling", "Compliance", "npm publish", "Run tests"}, []bool{true, false, false, false}, 0},
		{"claude-2.1.283-tabs-w44-third.ansi",
			[]string{"To…", "Ch…", "Release ca…"}, []bool{false, false, false}, 2},
		{"claude-2.1.283-tabs-w80-submit-unanswered.ansi",
			[]string{"Tooling", "Compliance", "npm publish", "Run tests"}, []bool{false, false, false, false}, 4},
		{"claude-2.1.283-tabs-w80-review-complete.ansi",
			[]string{"Tooling", "Compliance", "npm publish", "Run tests"}, []bool{true, true, true, true}, 4},
		// Wrapped onto two lines: the glyphs are all on the first, the labels
		// are not, and nothing is keyed off a label.
		{"claude-2.1.283-tabs-w44-submit-wrapped.ansi",
			[]string{"", "Checks", "Release"}, []bool{false, false, false}, 3},
	} {
		t.Run(tc.file, func(t *testing.T) {
			got, ok := ParseStepper(fixture(t, tc.file))
			if !ok {
				t.Fatal("no stepper read")
			}
			var labels []string
			var answered []bool
			for _, step := range got.Steps {
				labels = append(labels, step.Label)
				answered = append(answered, step.Answered)
			}
			if !reflect.DeepEqual(labels, tc.labels) || !reflect.DeepEqual(answered, tc.answered) {
				t.Errorf("steps = %q %v, want %q %v", labels, answered, tc.labels, tc.answered)
			}
			if got.Active != tc.active {
				t.Errorf("active = %d, want %d", got.Active, tc.active)
			}
			// Stripped, the tabs are still there; only the active one is lost.
			plain, ok := ParseStepper(ansi.Strip(fixture(t, tc.file)))
			if !ok || len(plain.Steps) != len(tc.labels) || plain.Active != -1 {
				t.Errorf("stripped: ok = %v steps = %d active = %d", ok, len(plain.Steps), plain.Active)
			}
		})
	}
}

func TestParseStepperIgnoresASingleQuestion(t *testing.T) {
	if _, ok := ParseStepper(capturedAskPane); ok {
		t.Error("a single-question dialog read as tabbed")
	}
}

func TestParseReviewReadsTheAnswersAboutToBeSent(t *testing.T) {
	got, ok := ParseReview(ansi.Strip(fixture(t, "claude-2.1.283-tabs-w80-review-complete.ansi")))
	if !ok {
		t.Fatal("review page not read")
	}
	want := []ReviewAnswer{
		{"Which package manager should the new workspace standardise on for every JavaScript package?", "pnpm"},
		{"Should the release pipeline block on the licence compliance scan?", "Warn for a month, then block"},
		{"Which registry should packages publish to?", "Public npm"},
		{"Where should the integration tests run?", "Nightly"},
	}
	if !reflect.DeepEqual(got.Answers, want) {
		t.Errorf("answers = %q", got.Answers)
	}
	if !got.Complete || got.Cursor != 1 || got.Submit != 1 {
		t.Errorf("complete = %v cursor = %d submit = %d", got.Complete, got.Cursor, got.Submit)
	}
	if _, ok := Inspect(ansi.Strip(fixture(t, "claude-2.1.283-tabs-w80-review-complete.ansi"))); ok {
		t.Error("the review page parsed as a question dialog")
	}
}

func TestParseReviewSeesTheUnansweredWarning(t *testing.T) {
	got, ok := ParseReview(ansi.Strip(fixture(t, "claude-2.1.283-tabs-w80-submit-unanswered.ansi")))
	if !ok {
		t.Fatal("review page not read")
	}
	if got.Complete || len(got.Answers) != 0 {
		t.Errorf("complete = %v answers = %q", got.Complete, got.Answers)
	}
}

func TestUnwrapLegendLeavesProseAlone(t *testing.T) {
	pane := "Enter to select the tier you want\nand then continue\n"
	if got := unwrapLegend(pane); got != pane {
		t.Errorf("prose rewritten: %q", got)
	}
}

// The conversation above a dialog is not its question. Measured live: the
// last line of the prompt that raised the dialog sat directly above its rule,
// inside the window the prompt is read from, and was read as the question's
// first line -- so no tab ever matched the question the transcript named.
func TestThePromptStopsAtTheDialogsTopEdge(t *testing.T) {
	pane := "  All single-select.\n" + ansi.Strip(fixture(t, "claude-2.1.283-tabs-w50-first.ansi"))
	got, ok := Parse(pane)
	if !ok {
		t.Fatal("did not parse")
	}
	if want := "Which package manager should the new workspace\nstandardise on for every JavaScript package?"; got.Prompt != want {
		t.Errorf("prompt = %q, want %q", got.Prompt, want)
	}
}
