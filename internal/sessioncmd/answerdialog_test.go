package sessioncmd

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
)

// tabbedDialog models the several-question AskUserQuestion Claude Code
// 2.1.283 draws, as measured on a live pane (the captures are
// internal/dialog/testdata/claude-2.1.283-tabs-*):
//
//   - Right, Left and Tab move between tabs and touch no answer; entering a
//     tab puts the cursor on its first row. None of them moves while the
//     cursor is on a free-text row holding words.
//   - Enter on a choice answers the question, ticks its tab and moves to the
//     next tab; after the last question that is the Submit tab.
//   - Words pasted with the cursor anywhere but the free-text row are
//     dropped, and the Enter after them picks the row under the cursor.
//   - The Submit tab's review page lists the answers; Enter on "Submit
//     answers" closes the dialog.
type tabbedDialog struct {
	questions []convo.AskQuestion
	answers   []string
	active    int
	cursor    int
	typing    string
	closed    bool
	keys      []string
	// single draws one question with no row of tabs, as a one-question call
	// does: Enter on a choice answers it and closes the dialog.
	single bool
	// width wraps the prompt, the choices, the review page and the record of
	// answers the way a narrow pane does; 0 draws every line whole.
	width int
	// prose is the turn above the dialog, drawn over its top edge.
	prose []string
	// shift is a fault: Enter on a choice registers the one shift rows below
	// the cursor, the way a dialog keyed from a stale position would.
	shift int
	// silent is a fault: the dialog closes without printing its record.
	silent bool
	// ticked is each multi-select question's checked boxes, by 1-based row.
	// A multi-select is drawn as Claude Code 2.1.286 draws one: checkboxes,
	// "[ ] Type something", an unnumbered Submit row, then Chat about this.
	ticked map[int]map[int]bool
	// stuck is a fault: Enter on this 1-based box of a multi-select does not
	// toggle it.
	stuck int
}

func newTabbedDialog(questions []convo.AskQuestion) *tabbedDialog {
	return &tabbedDialog{questions: questions, answers: make([]string, len(questions)), cursor: 1}
}

func newSingleDialog(question convo.AskQuestion, width int, prose ...string) *tabbedDialog {
	d := newTabbedDialog([]convo.AskQuestion{question})
	d.single, d.width, d.prose = true, width, prose
	return d
}

func (d *tabbedDialog) rows() int {
	if d.active == len(d.questions) {
		return 2
	}
	if d.multi() {
		return len(d.questions[d.active].Options) + 3
	}
	return len(d.questions[d.active].Options) + 2
}

func (d *tabbedDialog) multi() bool {
	return d.active < len(d.questions) && d.questions[d.active].MultiSelect
}

func (d *tabbedDialog) box(n int) bool { return d.ticked[d.active][n] }

func (d *tabbedDialog) freeText() int { return len(d.questions[d.active].Options) + 1 }

// wrap breaks text into lines of at most the model's width, the first after
// first and the rest after indent. A word wider than the text column is
// broken wherever a row ends, as Claude Code breaks a long path.
func (d *tabbedDialog) wrap(first, indent, text string) string {
	if d.width == 0 {
		return first + text + "\n"
	}
	var out strings.Builder
	line := first
	fresh := true
	for _, word := range strings.Fields(text) {
		if runes := []rune(word); len(runes) > d.width-len([]rune(indent)) {
			if !fresh {
				line += " "
			}
			for len(runes) > 0 {
				room := d.width - len([]rune(line))
				if room <= 0 {
					out.WriteString(line + "\n")
					line, room = indent, d.width-len([]rune(indent))
				}
				take := min(room, len(runes))
				line, runes = line+string(runes[:take]), runes[take:]
			}
			fresh = false
			continue
		}
		if !fresh && len([]rune(line))+1+len([]rune(word)) > d.width {
			out.WriteString(line + "\n")
			line, fresh = indent, true
		}
		if !fresh {
			line += " "
		}
		line += word
		fresh = false
	}
	out.WriteString(line + "\n")
	return out.String()
}

func (d *tabbedDialog) rule() string {
	if d.width == 0 {
		return strings.Repeat("─", 40) + "\n"
	}
	return strings.Repeat("─", d.width) + "\n"
}

func (d *tabbedDialog) Capture() (string, error) {
	var out strings.Builder
	for _, line := range d.prose {
		out.WriteString(d.wrap("  ", "     ", line))
	}
	if d.closed {
		if d.silent {
			return out.String() + "\n❯ \n", nil
		}
		out.WriteString("\n● User answered Claude's questions:\n")
		for i, q := range d.questions {
			lead := "     · "
			if i == 0 {
				lead = "  ⎿  · "
			}
			out.WriteString(d.wrap(lead, "     ", q.Question+" → "+d.answers[i]))
		}
		out.WriteString("\n❯ \n")
		return out.String(), nil
	}
	out.WriteString(d.rule())
	if d.single {
		out.WriteString(" ☐ " + d.questions[0].Header + "\n\n")
	} else {
		out.WriteString("←  ")
		for i, q := range d.questions {
			glyph := "☐"
			if d.answers[i] != "" {
				glyph = "☒"
			}
			entry := glyph + " " + q.Header
			if i == d.active {
				out.WriteString("\x1b[38;5;16m\x1b[48;5;153m " + entry + " \x1b[39m\x1b[49m ")
			} else {
				out.WriteString(" " + entry + "  ")
			}
		}
	}
	if !d.single && d.active == len(d.questions) {
		out.WriteString("\x1b[38;5;16m\x1b[48;5;153m ✔ Submit \x1b[38;5;246m\x1b[49m →\n")
		out.WriteString("Review your answers\n")
		complete := true
		for i, q := range d.questions {
			if d.answers[i] == "" {
				complete = false
				continue
			}
			out.WriteString(d.wrap(" ● ", "   ", q.Question))
			out.WriteString(d.wrap("   → ", "   ", d.answers[i]))
		}
		if !complete {
			out.WriteString("⚠ You have not answered all questions\n")
		}
		out.WriteString("Ready to submit your answers?\n")
		for n, label := range []string{"Submit answers", "Cancel"} {
			marker := "  "
			if n+1 == d.cursor {
				marker = "❯ "
			}
			fmt.Fprintf(&out, "%s%d. %s\n", marker, n+1, label)
		}
		return out.String(), nil
	}
	if !d.single {
		out.WriteString(" ✔ Submit  →\n")
	}
	q := d.questions[d.active]
	out.WriteString(d.wrap("", "", q.Question))
	row := func(n int, label string) {
		marker := "  "
		if n == d.cursor {
			marker = "❯ "
		}
		out.WriteString(d.wrap(fmt.Sprintf("%s%d. ", marker, n), "     ", label))
	}
	if q.MultiSelect {
		for n, option := range q.Options {
			box := "[ ] "
			if d.box(n + 1) {
				box = "[✔] "
			}
			row(n+1, box+option.Label)
			out.WriteString(d.wrap("         ", "         ", option.Description))
		}
		row(d.freeText(), "[ ] Type something")
		marker := "  "
		if d.cursor == d.freeText()+1 {
			marker = "❯ "
		}
		out.WriteString(marker + "   Submit\n")
		out.WriteString(d.rule())
		marker = "  "
		if d.cursor == d.freeText()+2 {
			marker = "❯ "
		}
		out.WriteString(d.wrap(fmt.Sprintf("%s%d. ", marker, d.freeText()+1), "     ", "Chat about this"))
		out.WriteString(d.wrap("", "", "Enter to select · ↑/↓ to navigate · Esc to cancel"))
		return out.String(), nil
	}
	for n, option := range q.Options {
		label := option.Label
		if d.answers[d.active] == option.Label {
			label += " ✔"
		}
		row(n+1, label)
		out.WriteString(d.wrap("     ", "     ", option.Description))
	}
	free := "Type something."
	if d.typing != "" {
		free = d.typing
	}
	row(d.freeText(), free)
	out.WriteString(d.rule())
	row(d.freeText()+1, "Chat about this")
	legend := "Enter to select · Tab/Arrow keys to navigate · Esc to cancel"
	if d.single {
		legend = "Enter to select · ↑/↓ to navigate · Esc to cancel"
	}
	out.WriteString(d.wrap("", "", legend))
	return out.String(), nil
}

func (d *tabbedDialog) Keys(keys ...string) error {
	for _, key := range keys {
		d.keys = append(d.keys, key)
		if d.closed {
			continue
		}
		onFreeText := d.active < len(d.questions) && d.cursor == d.freeText()
		switch key {
		case "Right", "Tab", "Left":
			if d.single || onFreeText && d.typing != "" {
				continue
			}
			if key == "Left" && d.active > 0 {
				d.active--
			} else if key != "Left" && d.active < len(d.questions) {
				d.active++
			}
			d.cursor, d.typing = 1, ""
		case "Down":
			d.cursor = min(d.cursor+1, d.rows())
		case "Up":
			d.cursor = max(d.cursor-1, 1)
		case "Enter":
			d.enter()
		}
	}
	return nil
}

func (d *tabbedDialog) enter() {
	if d.active == len(d.questions) {
		if d.cursor == 1 {
			d.closed = true
		}
		return
	}
	q := d.questions[d.active]
	if q.MultiSelect {
		switch {
		case d.cursor <= len(q.Options):
			if d.cursor == d.stuck {
				return
			}
			if d.ticked == nil {
				d.ticked = map[int]map[int]bool{}
			}
			if d.ticked[d.active] == nil {
				d.ticked[d.active] = map[int]bool{}
			}
			d.ticked[d.active][d.cursor] = !d.ticked[d.active][d.cursor]
			return
		case d.cursor == d.freeText()+1:
			var picked []string
			for n, option := range q.Options {
				if d.box(n + 1) {
					picked = append(picked, option.Label)
				}
			}
			if len(picked) == 0 {
				return
			}
			d.answers[d.active] = strings.Join(picked, ", ")
			d.active++
			d.cursor, d.typing = 1, ""
		}
		return
	}
	switch {
	case d.cursor <= len(q.Options):
		d.answers[d.active] = q.Options[min(d.cursor+d.shift, len(q.Options))-1].Label
	case d.cursor == d.freeText() && d.typing != "":
		d.answers[d.active] = d.typing
	default:
		return
	}
	if d.single {
		d.closed = true
		return
	}
	d.active++
	d.cursor, d.typing = 1, ""
}

func (d *tabbedDialog) Type(text string) error {
	d.keys = append(d.keys, "paste:"+text)
	if d.active < len(d.questions) && d.cursor == d.freeText() {
		d.typing = text
	}
	d.enter()
	return nil
}

// fourQuestions is the dialog the report was about.
var fourQuestions = []convo.AskQuestion{
	{Header: "Tooling", Question: "Which package manager should the new workspace standardise on?",
		Options: []convo.AskOption{{Label: "Bun", Description: "Fast, already used in CI"}, {Label: "pnpm", Description: "Strict hoisting"}, {Label: "npm", Description: "Default, slowest"}}},
	{Header: "Compliance", Question: "Should the release pipeline block on the licence compliance scan?",
		Options: []convo.AskOption{{Label: "Block", Description: "Fail the release"}, {Label: "Warn", Description: "Annotate only"}}},
	{Header: "npm publish", Question: "Which registry should packages publish to?",
		Options: []convo.AskOption{{Label: "Public npm", Description: "registry.npmjs.org"}, {Label: "GitHub Packages", Description: "Scoped, private"}}},
	{Header: "Run tests", Question: "Where should the integration tests run?",
		Options: []convo.AskOption{{Label: "CI only", Description: "GitHub Actions"}, {Label: "Locally too", Description: "Also on dev machines"}, {Label: "Nightly", Description: "Scheduled job"}}},
}

func fastSettle(t *testing.T) {
	t.Helper()
	timeout, poll, readback := settleTimeout, settlePoll, readbackTimeout
	settleTimeout, settlePoll, readbackTimeout = 200*time.Millisecond, time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { settleTimeout, settlePoll, readbackTimeout = timeout, poll, readback })
}

func questionsOf(t *testing.T, pane dialogPane, asked []convo.AskQuestion) []dialog.Question {
	t.Helper()
	raw, err := pane.Capture()
	if err != nil {
		t.Fatal(err)
	}
	questions := dialog.Questions(raw, asked)
	if len(questions) == 0 {
		t.Fatalf("no questions read off:\n%s", raw)
	}
	return questions
}

func TestQuestionsReadsEveryTabWithoutAKeystroke(t *testing.T) {
	sim := newTabbedDialog(fourQuestions)
	sim.answers[0] = "pnpm"
	sim.active = 1
	questions := questionsOf(t, sim, fourQuestions)
	if len(sim.keys) != 0 {
		t.Fatalf("reading pressed %v", sim.keys)
	}
	if len(questions) != 4 {
		t.Fatalf("read %d questions, want 4", len(questions))
	}
	if !questions[0].Answered || questions[1].Answered || !questions[1].OnScreen {
		t.Errorf("answered/on-screen wrong: %+v", questions)
	}
	if questions[3].Question != fourQuestions[3].Question || len(questions[3].Options) != 3 ||
		questions[3].Options[2].Description != "Scheduled job" {
		t.Errorf("question 4 = %+v, want it whole from the transcript", questions[3])
	}
	// Without a transcript the one on the screen is still read whole, and the
	// rest by their tab labels.
	bare := questionsOf(t, sim, nil)
	if bare[1].Question != fourQuestions[1].Question || bare[3].Header != "Run tests" || bare[3].Question != "" {
		t.Errorf("pane-only read = %+v", bare)
	}
	if got := bare[1].Options; len(got) != 2 || got[1].Label != "Warn" {
		t.Errorf("pane-only options = %+v, want the child's two without the harness rows", got)
	}
}

func TestFillDialogAnswersEveryQuestionAndSubmits(t *testing.T) {
	fastSettle(t)
	sim := newTabbedDialog(fourQuestions)
	answered, err := fillDialog(sim, questionsOf(t, sim, fourQuestions), []QuestionAnswer{
		{Question: "Run tests", Answer: "Nightly"},
		{Question: "Tooling", Answer: "pnpm"},
		{Question: "2", Answer: "Warn for a month, then block"},
		{Question: "npm", Answer: "public npm"},
	}, true)
	if err != nil {
		t.Fatalf("fillDialog: %v (keys %v)", err, sim.keys)
	}
	want := []string{"pnpm", "Warn for a month, then block", "Public npm", "Nightly"}
	if !reflect.DeepEqual(sim.answers, want) {
		t.Errorf("dialog holds %q, want %q", sim.answers, want)
	}
	if !sim.closed || !answered.Submitted || answered.Standing != 0 {
		t.Errorf("closed = %v submitted = %v standing = %d", sim.closed, answered.Submitted, answered.Standing)
	}
	if len(answered.Answers) != 4 || answered.Answers[1].Selected != "" || answered.Answers[2].Selected != "Public npm" {
		t.Errorf("answers = %+v", answered.Answers)
	}
}

// Leaving a question out leaves it standing, keeps whatever answer another
// hand already gave, and does not submit.
func TestFillDialogLeavesTheRestStanding(t *testing.T) {
	fastSettle(t)
	sim := newTabbedDialog(fourQuestions)
	sim.answers[2] = "GitHub Packages"
	answered, err := fillDialog(sim, questionsOf(t, sim, fourQuestions), []QuestionAnswer{
		{Question: "Compliance", Answer: "Block"},
	}, true)
	if err != nil {
		t.Fatalf("fillDialog: %v", err)
	}
	if want := []string{"", "Block", "GitHub Packages", ""}; !reflect.DeepEqual(sim.answers, want) {
		t.Errorf("dialog holds %q, want %q", sim.answers, want)
	}
	if sim.closed || answered.Submitted || answered.Standing != 2 {
		t.Errorf("closed = %v submitted = %v standing = %d", sim.closed, answered.Submitted, answered.Standing)
	}
	if !answered.Questions[2].Answered || answered.Questions[0].Answered {
		t.Errorf("questions after = %+v", answered.Questions)
	}
}

func TestFillDialogSubmitFalseStopsAtTheReviewPage(t *testing.T) {
	fastSettle(t)
	sim := newTabbedDialog(fourQuestions[:2])
	answered, err := fillDialog(sim, questionsOf(t, sim, fourQuestions[:2]), []QuestionAnswer{
		{Question: "1", Answer: "Bun"}, {Question: "2", Answer: "Warn"},
	}, false)
	if err != nil {
		t.Fatalf("fillDialog: %v", err)
	}
	if sim.closed || answered.Submitted {
		t.Error("submitted with submit false")
	}
}

// Every refusal comes before the first keystroke, so a batch with one bad
// entry changes nothing in the child's dialog.
func TestFillDialogRefusesBeforeTouchingThePane(t *testing.T) {
	fastSettle(t)
	multi := append([]convo.AskQuestion{}, fourQuestions...)
	multi[1] = convo.AskQuestion{Header: "Checks", Question: "Which checks?", MultiSelect: true,
		Options: []convo.AskOption{{Label: "Build"}, {Label: "Lint"}}}
	for name, tc := range map[string]struct {
		asked   []convo.AskQuestion
		answers []QuestionAnswer
		want    string
	}{
		"a multi-select":          {multi, []QuestionAnswer{{Question: "1", Answer: "Bun"}, {Question: "Checks", Answer: "Build"}}, "multi-select"},
		"an unknown question":     {fourQuestions, []QuestionAnswer{{Question: "1", Answer: "Bun"}, {Question: "Deploys", Answer: "Friday"}}, "names none"},
		"a question out of range": {fourQuestions, []QuestionAnswer{{Question: "7", Answer: "Bun"}}, "does not exist"},
		"one question twice":      {fourQuestions, []QuestionAnswer{{Question: "1", Answer: "Bun"}, {Question: "Tooling", Answer: "npm"}}, "twice"},
	} {
		t.Run(name, func(t *testing.T) {
			sim := newTabbedDialog(tc.asked)
			_, err := fillDialog(sim, questionsOf(t, sim, tc.asked), tc.answers, true)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one saying %q", err, tc.want)
			}
			if len(sim.keys) != 0 {
				t.Errorf("keys were pressed before the refusal: %v", sim.keys)
			}
		})
	}
}

// "Chat about this" drops the dialog for the conversation; picking it is not
// an answer.
func TestFillDialogRefusesChatAboutThis(t *testing.T) {
	fastSettle(t)
	sim := newTabbedDialog(fourQuestions)
	_, err := fillDialog(sim, questionsOf(t, sim, fourQuestions), []QuestionAnswer{{Question: "1", Answer: "Chat about this"}}, true)
	if err == nil || !strings.Contains(err.Error(), "free-text row") {
		t.Fatalf("err = %v", err)
	}
	if sim.answers[0] != "" || sim.closed {
		t.Error("the dialog was changed")
	}
}

// Somebody typing into a free-text row pins the dialog to that tab. The
// sequence stops there rather than pressing on, and their words are kept.
func TestFillDialogStopsAtWordsBeingTyped(t *testing.T) {
	fastSettle(t)
	sim := newTabbedDialog(fourQuestions)
	sim.cursor, sim.typing = sim.freeText(), "half an answ"
	answered, err := fillDialog(sim, questionsOf(t, sim, fourQuestions), []QuestionAnswer{{Question: "3", Answer: "Public npm"}}, true)
	if err == nil || !strings.Contains(err.Error(), "did not move") {
		t.Fatalf("err = %v", err)
	}
	if sim.typing != "half an answ" || sim.answers[2] != "" {
		t.Errorf("typing = %q answers = %q", sim.typing, sim.answers)
	}
	if answered.Standing != 4 {
		t.Errorf("standing = %d, want all four", answered.Standing)
	}
}

// The measured trap behind typing: words pasted off the free-text row are
// dropped and the Enter picks the option under the cursor. The model does
// that, so this proves the cursor is moved first.
func TestTypedAnswersLandInTheFreeTextRow(t *testing.T) {
	fastSettle(t)
	sim := newTabbedDialog(fourQuestions)
	if err := sim.Type("dropped"); err != nil {
		t.Fatal(err)
	}
	if sim.answers[0] != "Bun" {
		t.Fatalf("the model does not reproduce the trap: %q", sim.answers)
	}
	sim = newTabbedDialog(fourQuestions)
	if _, err := fillDialog(sim, questionsOf(t, sim, fourQuestions), []QuestionAnswer{{Question: "1", Answer: "Use Bazel"}}, true); err != nil {
		t.Fatal(err)
	}
	if sim.answers[0] != "Use Bazel" {
		t.Errorf("answer = %q", sim.answers[0])
	}
}

func TestResolveAcceptsTruncatedTabLabels(t *testing.T) {
	questions := []dialog.Question{{Index: 1, Header: "Too…"}, {Index: 2, Header: "Compliance"}, {Index: 3, Header: "npm…"}}
	for ref, want := range map[string]int{"Tooling": 0, "compliance": 1, "npm publish": 2, "#2": 1, "3": 2, "Comp": 1} {
		got, err := dialog.Resolve(questions, ref)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %d, %v; want %d", ref, got, err, want)
		}
	}
}
