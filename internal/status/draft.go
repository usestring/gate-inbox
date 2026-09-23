package status

import (
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// composerReach is how many rows above the caret the prompt marker may sit:
// past the tallest composer any of these tools draws, short of the content
// above it, where a quoted marker in the transcript would otherwise pass for
// the prompt.
const composerReach = 12

// OperatorQuiet is how long the operator's last keystroke into a pane has
// to be behind before the manager types into it. A draft check reads the
// screen, and the screen is where the person was a moment ago: a key that
// lands between that read and the paste is submitted with the sender's
// text. The keystroke itself is known sooner than its echo -- from the
// manager's own forwarding when the pane is in the focus view, from tmux's
// session activity when a terminal is attached to it -- and this is the
// window either has to be clear of. One value, read by the poller that
// keeps the hold and by the reason a sender is given for it.
const OperatorQuiet = 3 * time.Second

// DraftInComposer reports whether anything is written in the tool's
// composer: after the prompt on its marker row, and on every row from there
// down to the caret. The caret row counts only what sits before the caret,
// since a tool draws its placeholder hint after it. The rows between count
// whole, because a composer only grows below its marker when a line was
// written there -- which is the case the caret row alone cannot see, a
// draft that wrapped or took a newline and left the caret on a row with no
// marker at its start. A boxed composer draws its bar on every row instead,
// so the composer is the whole run of marker rows the caret sits in, each
// read past its own bar.
func (e *Engine) DraftInComposer(tool, clean string, caretX, caretY int) bool {
	rows := strings.Split(clean, "\n")
	if caretY < 0 || caretY >= len(rows) {
		return false
	}
	// The composer is the rows the caret sits in: every marker row of a
	// box, or the one plain marker row and the written rows under it. A
	// blank row above the caret is outside it -- a composer only leaves one
	// blank where a newline was just taken, and that is the caret's own row.
	// Without that stop, a line terminal, whose prompt scrolls up as output
	// arrives and whose caret rests on an empty row far below, would read
	// its own history as a draft.
	top := -1
	for y := caretY; y >= 0 && y >= caretY-composerReach; y-- {
		if _, ok := e.InputPrefix(tool, rows[y]); ok {
			top = y
			continue
		}
		if y == caretY {
			continue
		}
		if top >= 0 || strings.TrimSpace(rows[y]) == "" {
			break
		}
	}
	if top < 0 {
		return false
	}
	// A caret resting on a blank row is a newline just taken, and a plain
	// composer draws that row directly under its marker. With written rows
	// between, the shape is a terminal that has printed under its prompt
	// and rests below the output; the one draft it also fits, a wrapped
	// line with a newline after it, is given up to keep history out.
	if strings.TrimSpace(rows[caretY]) == "" && top != caretY-1 {
		return false
	}
	for y := top; y <= caretY; y++ {
		from := 0
		if prefix, ok := e.InputPrefix(tool, rows[y]); ok {
			from = ansi.StringWidth(prefix)
		}
		to := -1
		if y == caretY {
			to = caretX
		}
		if TextBetweenCells(rows[y], from, to) {
			return true
		}
	}
	return false
}

// TextBetweenCells reports whether anything but blanks sits in the row
// between two display columns, to the end of the row when to is negative.
// Columns are what tmux reports a caret in, so the walk is in cells.
func TextBetweenCells(row string, from, to int) bool {
	line := []rune(row)
	for cell := from; to < 0 || cell < to; {
		index := runeAtColumn(line, cell)
		if index >= len(line) {
			return false
		}
		if !unicode.IsSpace(line[index]) {
			return true
		}
		cell += ansi.StringWidth(string(line[index]))
	}
	return false
}

// runeAtColumn finds which rune sits at a display column, since tmux
// reports the caret in cells and a wide rune covers two.
func runeAtColumn(line []rune, column int) int {
	cell := 0
	for i, r := range line {
		next := cell + ansi.StringWidth(string(r))
		if column < next {
			return i
		}
		cell = next
	}
	return len(line)
}
