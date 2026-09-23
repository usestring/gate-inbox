package status

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// DialogStepIsFirst reports whether a question dialog's stepper is parked on
// its leftmost entry, and whether there was a stepper to read at all.
//
// It is what decides Left on a dialog that draws one. The legend cannot: the
// same three-question dialog advertises "Tab/Arrow keys to navigate" while a
// multi-select one says "↑/↓ to navigate · n to add notes · Tab to switch
// questions", and both take Left as "the previous question". Position is the
// honest signal, and it is exactly what the operator sees -- measured on a
// live dialog, Left one entry in steps back, and Left on the first entry
// leaves the pane byte for byte unchanged, so there it costs the dialog
// nothing to hand the key to the board instead.
//
// pane must be the raw capture. The active entry is drawn on a background
// colour and nothing else on the row is, so the SGR run is the marker; which
// colour it happens to be is not read, only that it sets a background.
func (e *Engine) DialogStepIsFirst(tool, pane string) (first, ok bool) {
	tr, known := e.tools[tool]
	if !known || tr.stepRow == nil || tr.stepEntry == nil {
		return false, false
	}
	row, found := tr.stepperRow(pane)
	if !found {
		return false, false
	}
	active := firstBackgroundSGR(row)
	if active < 0 {
		return false, false
	}
	// An entry drawn before the highlighted one is a step to the left, which
	// is where Left goes.
	return !tr.stepEntry.MatchString(ansi.Strip(row[:active])), true
}

// DialogStepIsLast reports whether a question dialog's stepper is parked on
// its rightmost entry, and whether there was a stepper to read at all.
//
// The last entry is the one that submits. Measured on a live dialog, Enter on
// any other entry is a step along the way: on a single-select question it
// picks the row and moves the stepper on, on a multi-select one it ticks the
// row's box and stays. Only the review page -- "Ready to submit your
// answers?" under a highlighted Submit -- answers the dialog on Enter, and
// the stepper is the one thing that says the dialog is there rather than on
// a question, since the review page draws a numbered list like any other.
func (e *Engine) DialogStepIsLast(tool, pane string) (last, ok bool) {
	tr, known := e.tools[tool]
	if !known || tr.stepRow == nil || tr.stepEntry == nil {
		return false, false
	}
	row, found := tr.stepperRow(pane)
	if !found {
		return false, false
	}
	active := firstBackgroundSGR(row)
	if active < 0 {
		return false, false
	}
	// The highlighted run starts at the active entry's glyph; an entry drawn
	// after that glyph is a step to the right, so the active one is not last.
	rest := ansi.Strip(row[active:])
	glyph := tr.stepEntry.FindStringIndex(rest)
	if glyph == nil {
		return false, false
	}
	return !tr.stepEntry.MatchString(rest[glyph[1]:]), true
}

// stepperRow finds the stepper by its stripped text and returns the row with
// its escapes intact, since those carry which entry is active.
func (tr toolRules) stepperRow(pane string) (string, bool) {
	for _, row := range strings.Split(pane, "\n") {
		if tr.stepRow.MatchString(ansi.Strip(row)) {
			return row, true
		}
	}
	return "", false
}

var sgrPattern = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// firstBackgroundSGR is the index of the first escape on the row that sets a
// background colour -- 40-47, 100-107, or the 48 that introduces an indexed
// or truecolour one. A foreground reset leading the row (39) is not one, and
// neither is any of the resets that close the run.
func firstBackgroundSGR(row string) int {
	for _, at := range sgrPattern.FindAllStringSubmatchIndex(row, -1) {
		for _, field := range strings.Split(row[at[2]:at[3]], ";") {
			code, err := strconv.Atoi(field)
			if err != nil {
				continue
			}
			if code == 48 || (code >= 40 && code <= 47) || (code >= 100 && code <= 107) {
				return at[0]
			}
		}
	}
	return -1
}
