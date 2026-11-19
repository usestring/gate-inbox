package dialog

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/convo"
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

// A review page taller than the pane, measured live on Claude Code 2.1.321
// (2026-11-12, renamed): the heading is still on the
// screen, the tab row above it is not.
const reviewNoTabRow = `Review your answers

 │ ● example/api PR #42 fails the public-docs
 │   check. Approve the re-record?
   → Approve re-record
 │ ● Ship #42 once it's green?
   → Merge+deploy when green
 │ ● How should I proceed with the load test?
   → Pilot 300 turns first (Recommended)

Ready to submit your answers?

❯ 1. Submit answers
  2. Cancel
`

func TestAReviewPageWithoutItsTabRowStillCountsEveryQuestion(t *testing.T) {
	review, ok := OnReview(reviewNoTabRow)
	if !ok || review.Clipped || len(review.Answers) != 3 {
		t.Fatalf("review = %+v ok = %v", review, ok)
	}
	questions := Questions(reviewNoTabRow, nil)
	if len(questions) != 3 || !review.Done(questions) {
		t.Fatalf("questions = %+v, done = %v; want all three answered", questions, review.Done(questions))
	}
	if questions[2].Answer != "Pilot 300 turns first (Recommended)" {
		t.Errorf("question 3 answer = %q", questions[2].Answer)
	}
}

func TestAClippedReviewPageNeedsItsChoicesAtTheFoot(t *testing.T) {
	prose := "   → Deploy search\nI asked: Ready to submit your answers?\n\n❯ 1. Submit answers\n  2. Cancel\n\nmore output\n"
	if _, ok := ParseReview(prose); ok {
		t.Error("prose quoting the review page read as one")
	}
}

// A review page Claude Code 2.1.328 scrolled inside its own box, measured
// live in a 110x22 pane with four long questions: the box draws "↑ 6 more
// lines" over the choices, so "Ready to submit your answers?" is out of sight
// as well as the heading and the tab row.
const reviewScrolledInBox = ` │   recorded in the rollout log, is read back by the next session that picks up this work, and cannot be
 │   changed later without opening a new review, so please pick the option you would still stand behind next
 │   week.
   → North
 │ ● Round A: how often should the survey run once it starts? Before you answer, keep in mind that this choice
 │   is recorded in the rollout log, is read back by the next session that picks up this work, and cannot be
 │   changed later without opening a new review, so please pick the option you would still stand behind next
 │   week.
   → Weekly
 │ ● Round A: which format should the survey report use? Before you answer, keep in mind that this choice is
 │   recorded in the rollout log, is read back by the next session that picks up this work, and cannot be
 │   changed later without opening a new review, so please pick the option you would still stand behind next
 │   week.
   → Table
 ↑ 6 more lines
❯ 1. Submit answers
  2. Cancel
`

func TestAReviewPageScrolledInItsBoxReadsAsAReviewPage(t *testing.T) {
	review, ok := OnReview(reviewScrolledInBox)
	if !ok || !review.Clipped || review.Submit != 1 || review.Cursor != 1 {
		t.Fatalf("review = %+v ok = %v", review, ok)
	}
	var answers []string
	for _, answer := range review.Answers {
		answers = append(answers, answer.Answer)
	}
	if want := []string{"North", "Weekly", "Table"}; !slices.Equal(answers, want) {
		t.Errorf("answers = %q, want %q", answers, want)
	}
}

func TestAScrollMarkerOverOtherChoicesIsNotAReviewPage(t *testing.T) {
	pane := "   → North\n ↑ 6 more lines\n❯ 1. Yes\n  2. No\n"
	if _, ok := ParseReview(pane); ok {
		t.Error("a scroll marker over choices other than Submit read as a review page")
	}
}

// The page the e2e run against Claude Code 2.1.328 stopped on, in a 110x22
// pane: four questions that all end with the same long sentence, the first
// scrolled off but for that sentence. Its tail reads as the end of every
// question, so before Placed answer_session took it for question 2's row and
// refused the batch: question 2 "Weekly" registered as "North".
const reviewSharedEnding = ` │   recorded in the rollout log, is read back by the next session that picks up this work, and cannot be
 │   changed later without opening a new review, so please pick the option you would still stand behind next
 │   week.
   → North
 │ ● Round A: how often should the survey run once it starts? Before you answer, keep in mind that this choice
 │   is recorded in the rollout log, is read back by the next session that picks up this work, and cannot be
 │   changed later without opening a new review, so please pick the option you would still stand behind next
 │   week.
   → Weekly
 │ ● Round A: which team should own the survey results? Before you answer, keep in mind that this choice is
 │   recorded in the rollout log, is read back by the next session that picks up this work, and cannot be
 │   changed later without opening a new review, so please pick the option you would still stand behind next
 │   week.
   → Research
 │ ● Round A: which format should the survey report use? Before you answer, keep in mind that this choice is
 │   recorded in the rollout log, is read back by the next session that picks up this work, and cannot be
 │   changed later without opening a new review, so please pick the option you would still stand behind next
 │   week.
   → Table
 ↑ 6 more lines
❯ 1. Submit answers
  2. Cancel
`

func sharedEnding(subjects ...string) []string {
	var out []string
	for _, subject := range subjects {
		out = append(out, "Round A: "+subject+" Before you answer, keep in mind that this choice is recorded "+
			"in the rollout log, is read back by the next session that picks up this work, and cannot be changed "+
			"later without opening a new review, so please pick the option you would still stand behind next week.")
	}
	return out
}

func TestAPartialRowIsPlacedByTheRowAfterItNotByItsEnding(t *testing.T) {
	review, ok := OnReview(reviewSharedEnding)
	if !ok || !review.Tail || len(review.Answers) != 4 {
		t.Fatalf("review = %+v ok = %v", review, ok)
	}
	questions := sharedEnding("which region should the survey cover first?",
		"how often should the survey run once it starts?", "which team should own the survey results?",
		"which format should the survey report use?")
	placed := review.Placed(questions)
	for i, want := range []string{"North", "Weekly", "Research", "Table"} {
		if placed.Answers[i].Answer != want || !SameText(placed.Answers[i].Question, questions[i]) {
			t.Errorf("row %d = %+v, want question %d answered %q", i, placed.Answers[i], i+1, want)
		}
	}

	// With a fifth question scrolled off whole above it, the tail is still
	// question 1's, not question 0's, though both end the same way.
	five := append(sharedEnding("which budget should the survey use?"), questions...)
	placed = review.Placed(five)
	if placed.Answers[0].Answer != "North" || !SameText(placed.Answers[0].Question, five[1]) {
		t.Errorf("first row = %+v, want the region question", placed.Answers[0])
	}

	// read_session's view names each question's own answer.
	var asked []convo.AskQuestion
	for _, q := range five {
		asked = append(asked, convo.AskQuestion{Question: q})
	}
	got := Questions(reviewSharedEnding, asked)
	var answers []string
	for _, q := range got {
		answers = append(answers, q.Answer)
	}
	if want := []string{"", "North", "Weekly", "Research", "Table"}; !slices.Equal(answers, want) {
		t.Errorf("answers = %q, want %q", answers, want)
	}
	if !got[0].Answered {
		t.Error("the question scrolled off whole above the page should read as answered")
	}
}

func TestAPartialRowThatCannotBePlacedIsDropped(t *testing.T) {
	review, _ := OnReview(reviewSharedEnding)
	placed := review.Placed(sharedEnding("an unrelated question?"))
	if placed.Tail || len(placed.Answers) != 3 || placed.Answers[0].Answer != "Weekly" {
		t.Errorf("placed = %+v, want the partial row dropped", placed)
	}
}
