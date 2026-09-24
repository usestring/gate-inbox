// Package dialog reads the question dialogs agent CLIs draw in a pane: what
// is asked, the choices, and which keystrokes answer it. answer_session,
// read_session and the child-question relay all read a dialog through it.
package dialog

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The kinds are distinguishable on the screen, and the status rules already
// distinguish them: a permission prompt closes with "Enter to confirm",
// AskUserQuestion draws a keybinding legend naming Esc to cancel, and Codex
// closes its own three dialogs with three more. Those legends are the signal
// here too, so the readings cannot drift. Kind, in shape.go, is the set and
// why each is told apart.
//
// A permission or elicitation prompt stays the dialog guard. It asks whether
// this program may do something, which is the human's to answer. It is parsed
// rather than skipped, so that refusal can name it; Guarded is what keeps it
// unanswered.

// askLegend is the line AskUserQuestion draws under its options. The middle
// segment varies by dialog ("↑/↓ to navigate", "Tab/Arrow keys to navigate"),
// which is why it is matched rather than spelled.
var askLegend = regexp.MustCompile(
	`(?m)^[ \x{A0}]*Enter to select \x{B7} [^\x{B7}\n]+ to navigate \x{B7} Esc to cancel[ \x{A0}]*$`)

// askOption matches one numbered choice, with or without the cursor on it.
// Two marker glyphs, because two harnesses draw one: Claude Code's heavy
// angle and Codex's light one. A numbered line is only ever read under a
// legend, so a worker's own "1." in prose is not a choice here.
var askOption = regexp.MustCompile(`(?m)^[ \x{A0}]*([\x{276F}\x{203A}][ \x{A0}]+)?(\d+)\.[ \x{A0}]+(.+?)[ \x{A0}]*$`)

// askCheckbox is the box a multi-select draws in front of each of its
// choices. It is chrome, not part of the choice: left on, "[ ] Build" matches
// no answer any manager or operator would write, so the option could not be
// selected and the answer was typed at a pane that takes keystrokes.
var askCheckbox = regexp.MustCompile(`^\[[ xX*\x{2713}]?\][ \x{A0}]+`)

// askStepper is the row a multi-question dialog draws above its options: one
// box per question, ticked as each is answered, then Submit. Its presence is
// how this tells one question from several, and its boxes are how many are
// left.
var askStepper = regexp.MustCompile(`(?m)^[ \x{A0}]*\x{2190}[ \x{A0}]+(.*\x{2714}.*?)[ \x{A0}]*\x{2192}[ \x{A0}]*$`)

// Dialog is a question dialog as it stands on the screen.
type Dialog struct {
	// Kind is which harness drew it and which of that harness's dialogs it
	// is, read off the legend. It decides nothing about the options and
	// everything about whether they may be keyed: see Guarded and Refusal.
	Kind Kind
	// Prompt is what the worker asked, as the pane shows it.
	Prompt string
	// Options are the numbered choices in screen order.
	Options []string
	// Cursor is the 1-based option the marker sits on, or 0 when the pane does
	// not say which one that is: a dialog whose selection has scrolled off,
	// one drawn in a way this cannot read, or one showing the marker glyph on
	// more than one row. Nothing is keyed from 0, because moving from a
	// position this does not know would answer with whatever it landed on.
	Cursor int
	// MultiSelect marks a dialog whose choices are checkboxes. Enter toggles
	// one rather than answering, and the answer is only taken when Submit is
	// reached, so a single keystroke does not answer this dialog at all.
	MultiSelect bool
	// Steps is how many questions this one call is asking, and Answered how
	// many of them are ticked. 0 for a dialog with no stepper row, which is
	// the ordinary single question.
	Steps    int
	Answered int
}

// Standing is how many of the dialog's questions are still unanswered: 1 for
// an ordinary dialog, and the rest of the ladder for a stepper part way
// through.
func (d Dialog) Standing() int {
	if d.Steps == 0 {
		return 1
	}
	return d.Steps - d.Answered
}

// Parse reads out of a pane a dialog a keystroke could answer. ok is false
// for a pane holding none, which includes every permission prompt and
// Codex's first-run trust dialog -- both are read, and both are the human's.
//
// This is the reading every caller that acts on a dialog wants: the poller
// marking a child's wait answerable, and the relay that hands a parent its
// child's question. A caller that has to
// explain a refusal wants Inspect instead, which keeps the guarded ones.
func Parse(pane string) (Dialog, bool) {
	dialog, ok := Inspect(pane)
	if !ok || dialog.Guarded() {
		return Dialog{}, false
	}
	return dialog, true
}

// Inspect reads whatever dialog a pane is holding, guarded kinds
// included. ok is false only where there is no dialog on the pane at all.
//
// The split exists because "this is not a dialog" and "this is a dialog you
// may not answer" are two different things to do -- send words, or put it to a
// person -- and one refusal covering both sent every caller down the path that
// does not work. Kind has what that cost.
func Inspect(pane string) (Dialog, bool) {
	kind, legend := dialogLegend(pane)
	if legend == nil {
		return Dialog{}, false
	}
	// Only what is above the legend: a pane can carry an input line and an
	// older dialog's text below it, and the choices being answered are the
	// ones this legend belongs to.
	head := pane[:legend[0]]
	matches := askOption.FindAllStringSubmatch(head, -1)
	if len(matches) < 2 {
		// One choice is not a choice, and none means the options scrolled
		// away. Either way there is nothing here to select.
		return Dialog{}, false
	}
	dialog := Dialog{Kind: kind, Options: make([]string, 0, len(matches))}
	// More than one row can carry the marker glyph: Claude Code leaves a dim
	// one behind on the row the selection came from, and only the colour it is
	// drawn in tells that from the live one. The colour is gone by the time a
	// pane reaches here -- the event carries the stripped capture -- so a
	// second marker makes the position unknown rather than whichever came
	// last. Taking the last one read the cursor as 3 on a pane whose selection
	// was on 1, and two Up presses from there answer a question with whatever
	// they land on. See Choose: guessing here would pick a choice nobody made.
	markers := 0
	for _, match := range matches {
		if match[1] != "" {
			markers++
			if n, err := strconv.Atoi(match[2]); err == nil {
				dialog.Cursor = n
			}
		}
		option := strings.TrimSpace(match[3])
		if stripped := askCheckbox.ReplaceAllString(option, ""); stripped != option {
			dialog.MultiSelect = true
			option = strings.TrimSpace(stripped)
		}
		dialog.Options = append(dialog.Options, option)
	}
	if markers > 1 {
		dialog.Cursor = 0
	}
	dialog.Steps, dialog.Answered = countSteps(head)
	dialog.Prompt = dialogPrompt(head, askOption.FindStringIndex(head))
	return dialog, true
}

// countSteps reads the stepper row: how many questions this dialog is asking
// and how many are already ticked. Zeroes for a pane with no stepper row.
func countSteps(head string) (steps, answered int) {
	row := askStepper.FindStringSubmatch(head)
	if row == nil {
		return 0, 0
	}
	// Every box is a question; a ticked one has been answered. Submit is the
	// tick at the end of the row and is not a question, which is why the
	// boxes are counted rather than the segments.
	steps = strings.Count(row[1], "\u2610") + strings.Count(row[1], "\u2612")
	answered = strings.Count(row[1], "\u2612")
	return steps, answered
}

// dialogPrompt is the prose above the first option, which is the question
// itself. Bounded to the last few lines: the pane above it is the turn that
// led here, and the manager already has that as last_turn.
func dialogPrompt(head string, first []int) string {
	if first == nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(head[:first[0]], " \t\n"), "\n")
	const keep = 6
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// Question renders the dialog as the manager is asked about it: the question
// and the choices, in the words the worker put on the screen.
//
// It says how many questions are standing, because one call can ask several
// and answering the one on the screen only advances to the next. A manager
// told "the worker is holding a question" answers it and reports the ask
// dealt with; the worker is still waiting, on question two.
func (d Dialog) Question() string {
	var out strings.Builder
	if d.Prompt != "" {
		out.WriteString(d.Prompt)
		out.WriteString("\n")
	}
	switch {
	case d.MultiSelect:
		out.WriteString("\nIt is a multi-select dialog: the choices are checkboxes, and it is " +
			"answered by ticking any number of them and submitting, not by picking one. " +
			"The options are:\n")
	default:
		out.WriteString("\nIt is a multiple-choice dialog. The options are:\n")
	}
	for i, option := range d.Options {
		fmt.Fprintf(&out, "%d. %s\n", i+1, option)
	}
	if standing := d.Standing(); standing > 1 {
		fmt.Fprintf(&out, "\nThis is question %d of %d in one dialog: %d are still unanswered, and "+
			"answering this one moves to the next rather than ending the wait.\n",
			d.Answered+1, d.Steps, standing)
	}
	out.WriteString("\nAnswer with the text of the option to pick, or with your own words to type " +
		"an answer instead.")
	return out.String()
}

// harnessEscapes are the rows the harness appends to a dialog of its own
// accord: the free-text answer and the one that drops out of the dialog into
// the conversation. They are not the worker's choices, and relayed to the
// operator they are two rows that mean nothing -- AskUserQuestion already
// carries its own escape to typed words.
//
// Matched by their text, like the legend and the options above them: this
// package reads panes, and the strings are as much the harness's contract as
// the legend is.
var harnessEscapes = map[string]bool{
	"type something":  true,
	"type something.": true,
	"chat about this": true,
}

// Choices is the worker's own choices, which are the only answers that answer
// the dialog, with the rows the harness appends of its own accord left out, and
// at most max of them. Nil for a dialog whose rows are all the harness's own.
func (d Dialog) Choices(max int) []string {
	var options []string
	for _, option := range d.Options {
		if harnessEscapes[normalise(option)] {
			continue
		}
		if len(options) == max {
			break
		}
		options = append(options, option)
	}
	return options
}

// Choose finds the option an answer names, 1-based, or 0 for an answer that
// is none of them and should be typed.
//
// Exact first, then prefix, then containment, and each only when exactly one
// option matches -- a manager that answered "Keep the vendor solver" picks
// that option, and one that answered with a sentence of its own gets typed
// rather than mapped onto whichever option shares the most words with it.
// Guessing here would pick a choice nobody made.
func (d Dialog) Choose(answer string) int {
	answer = normalise(answer)
	if answer == "" {
		return 0
	}
	for _, match := range []func(option string) bool{
		func(option string) bool { return option == answer },
		func(option string) bool { return strings.HasPrefix(option, answer) },
		func(option string) bool { return strings.Contains(answer, option) },
	} {
		found, count := 0, 0
		for i, option := range d.Options {
			if match(normalise(option)) {
				found, count = i+1, count+1
			}
		}
		if count == 1 {
			return found
		}
	}
	return 0
}

func normalise(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// ErrNotKeyAnswerable is a dialog no keystroke this knows how to send will
// answer. The caller escalates rather than pretending it was answered.
var ErrNotKeyAnswerable = errors.New("dialog: this dialog cannot be answered by a keystroke")

// ErrNoDialog is a pane holding no dialog this can read: somebody answered it
// first, or the session is resting at its own input line.
var ErrNoDialog = errors.New("dialog: the pane is not holding a dialog")

// selectKeys is the keys that move the cursor from at to want and choose it.
// An unknown cursor position returns nothing, because moving blind would
// answer a question with whatever it landed on.
func selectKeys(at, want int) []string {
	if at <= 0 || want <= 0 {
		return nil
	}
	key, steps := "Down", want-at
	if steps < 0 {
		key, steps = "Up", -steps
	}
	keys := make([]string, 0, steps+1)
	for range steps {
		keys = append(keys, key)
	}
	return append(keys, "Enter")
}

// AnswerKeys is the keystrokes that put answer into dialog: the arrows and
// Enter that land on the option it names, or none for an answer that is
// nobody's option and has to be typed instead.
//
// It is the one reading of what a keystroke can answer, so every caller
// answering a dialog comes to the same conclusion about the same screen.
// ErrNotKeyAnswerable is a dialog none of them may touch, and Refusal is which of the four it is
// and why -- the sentinel says stop, and the phrase is what the caller tells
// whoever has to do something about it.
//
// One call answers one question, and a dialog asking several is answered by
// calling again rather than by taking a list. Two things settle it. The next
// question is not on the pane until this answer lands, so a second answer
// would be keyed against a screen this call has never read -- and the arrows
// it sends are counted from a cursor position, which is exactly the thing a
// stale reading gets wrong. And the steps of one dialog are not alike: a
// multi-select step is refused here whatever the steps around it are, so a
// batch would answer questions one and three, refuse two, and leave the dialog
// half-filled with no way to say which half. Standing, reported back as
// standing_questions, is how the caller knows to call again; the second call
// re-reads the pane, which is the correct-by-construction version of the same
// thing.
func AnswerKeys(dialog Dialog, answer string) ([]string, error) {
	if dialog.Refusal() != "" {
		return nil, ErrNotKeyAnswerable
	}
	return selectKeys(dialog.Cursor, dialog.Choose(answer)), nil
}
