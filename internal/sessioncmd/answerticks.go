package sessioncmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
)

// Answering a multi-select.
//
// Claude Code 2.1.286 draws a multi-select as a question tab whose options
// are checkboxes (measured at 40, 50 and 60 columns): Enter on an option
// ticks or unticks its box and leaves the cursor where it is, the free-text
// row comes after the options, an unnumbered "Submit" row after that, and
// Enter on Submit records the ticked boxes as the question's answer and moves
// on -- to the next question, or to the Submit tab's review page, which lists
// the answer as the ticked labels joined by ", " in option order.
//
// So a multi-select is answered box by box: every box whose state differs
// from the one wanted is visited and toggled, and each toggle is seen to land
// before the next key. Then the boxes are read back as a whole, and anything
// but exactly the wanted set is an error before Submit is pressed.

// tickOnScreen leaves exactly ticks ticked on the multi-select the pane is
// showing, reads the boxes back, and presses its Submit row.
func tickOnScreen(pane dialogPane, raw string, question dialog.Question, ticks []string) (FilledAnswer, error) {
	filled := FilledAnswer{Index: question.Index, Header: question.Header, question: question.Question}
	held, ok := dialog.Inspect(ansi.Strip(raw))
	if !ok {
		return filled, fmt.Errorf("%w: question %d is not on the screen", errDialogMoved, question.Index)
	}
	if held.Prompt != "" {
		filled.question = strings.Join(strings.Fields(held.Prompt), " ")
	}
	if question.Question != "" && !strings.EqualFold(filled.question, strings.Join(strings.Fields(question.Question), " ")) {
		return filled, fmt.Errorf("%w: the tab for question %d shows %q, not %q",
			errDialogMoved, question.Index, held.Prompt, question.Question)
	}
	if !held.MultiSelect {
		return filled, fmt.Errorf("question %d is not drawn as a multi-select, so no box was ticked", question.Index)
	}
	if held.FreeText == 0 || !held.SubmitRow {
		return filled, fmt.Errorf("question %d is a multi-select drawn without the free-text and Submit rows "+
			"this knows, so no box was ticked", question.Index)
	}
	boxes := held.FreeText - 1
	want := map[int]bool{}
	for _, tick := range ticks {
		n := held.ChooseLabel(tick)
		if n == 0 || n > boxes {
			return filled, fmt.Errorf("question %d: %q names none of its options (%s); nothing was ticked",
				question.Index, tick, strings.Join(held.Options[:boxes], ", "))
		}
		if want[n] {
			return filled, fmt.Errorf("question %d: %q is ticked twice in ticks", question.Index, held.Options[n-1])
		}
		want[n] = true
	}
	if held.OnSubmit {
		if err := pane.Keys("Up"); err != nil {
			return filled, err
		}
		var err error
		if held, err = readHeld(pane, func(d dialog.Dialog) bool { return d.Cursor == d.FreeText }); err != nil {
			return filled, fmt.Errorf("%w: the cursor did not leave question %d's Submit row", errDialogMoved, question.Index)
		}
	}
	for n := 1; n <= boxes; n++ {
		if slices.Contains(held.Ticked, n) == want[n] {
			continue
		}
		if held.Cursor == 0 {
			return filled, fmt.Errorf("question %d is %s", question.Index, held.Refusal())
		}
		if moves := dialog.SelectKeys(held.Cursor, n); len(moves) > 1 {
			if err := pane.Keys(moves[:len(moves)-1]...); err != nil {
				return filled, err
			}
			var err error
			if held, err = readHeld(pane, func(d dialog.Dialog) bool { return d.Cursor == n }); err != nil {
				return filled, fmt.Errorf("%w: the cursor never reached %q on question %d", errDialogMoved, held.Options[n-1], question.Index)
			}
		}
		if err := pane.Keys("Enter"); err != nil {
			return filled, err
		}
		var err error
		ticked := want[n]
		if held, err = readHeld(pane, func(d dialog.Dialog) bool { return slices.Contains(d.Ticked, n) == ticked }); err != nil {
			return filled, fmt.Errorf("question %d: Enter on %q did not toggle its box, so nothing was submitted",
				question.Index, held.Options[n-1])
		}
	}
	var got, wanted []string
	for n := 1; n <= boxes; n++ {
		if slices.Contains(held.Ticked, n) {
			got = append(got, held.Options[n-1])
		}
		if want[n] {
			wanted = append(wanted, held.Options[n-1])
		}
	}
	filled.Answer = strings.Join(ticks, ", ")
	filled.Selected = strings.Join(wanted, ", ")
	if !slices.Equal(got, wanted) {
		return filled, fmt.Errorf("%w: question %d was to have %q ticked, but its boxes read %q; nothing was submitted",
			errWrongAnswer, question.Index, wanted, got)
	}
	if held.Cursor != held.FreeText {
		moves := dialog.SelectKeys(held.Cursor, held.FreeText)
		if err := pane.Keys(moves[:len(moves)-1]...); err != nil {
			return filled, err
		}
	}
	if err := pane.Keys("Down"); err != nil {
		return filled, err
	}
	if _, err := readHeld(pane, func(d dialog.Dialog) bool { return d.OnSubmit }); err != nil {
		return filled, fmt.Errorf("%w: the cursor never reached question %d's Submit row, so it was not submitted",
			errDialogMoved, question.Index)
	}
	return filled, pane.Keys("Enter")
}

// readHeld captures the pane until the dialog on it satisfies done, and
// returns that reading.
func readHeld(pane dialogPane, done func(dialog.Dialog) bool) (dialog.Dialog, error) {
	var held dialog.Dialog
	_, err := waitFor(pane, func(raw string) bool {
		read, ok := dialog.Inspect(ansi.Strip(raw))
		if ok {
			held = read
		}
		return ok && done(read)
	})
	return held, err
}
