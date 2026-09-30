package dialog

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// What Claude Code prints once an AskUserQuestion is answered, captured from
// 2.1.284 at 40 and 50 columns:
//
//	● User answered Claude's questions:
//	  ⎿  · Which rollout path should I take? → Do both
//	     · Should I notify the channel when it lands?
//	     → Only after the deploy is verified in prod,
//	     then post
//
// It is the child's own record of what it took, so it is what an answer is
// checked against once the dialog has closed.

const answeredHeader = "User answered Claude's questions:"

// Claude Code wraps at spaces, and breaks a word only when the word is wider
// than the whole text column: at 40 columns a path comes out as "/tmp/s" over
// "ample-9000/...". So a row that ends short of the pane's width ended at a
// space, and so did a full one whose last word and the next row's first would
// have fit the column together, since that word would have moved down whole.
// Only a full row whose word could not have fit may have been broken inside
// it. That join is kept as wrapJoin, which the comparisons below read as
// either a space or nothing, and only there.

// wrapJoin marks where a full row may have been broken inside a word.
const wrapJoin = "\u200b"

// paneWidth is the widest row a pane draws, which is its width: every Claude
// Code pane draws a rule across it.
func paneWidth(lines []string) int {
	width := 0
	for _, line := range lines {
		width = max(width, ansi.StringWidth(strings.TrimRight(line, " \t\u00a0")))
	}
	return width
}

// continuation is how row joins next, the row it wrapped onto.
func continuation(row, next string, width int) string {
	row = strings.TrimRight(row, " \t\u00a0")
	if width == 0 || ansi.StringWidth(row) < width {
		return " "
	}
	before, after := strings.Fields(row), strings.Fields(next)
	if len(before) == 0 || len(after) == 0 {
		return " "
	}
	column := width - (ansi.StringWidth(next) - ansi.StringWidth(strings.TrimLeft(next, " \t\u00a0")))
	if ansi.StringWidth(before[len(before)-1]+after[0]) > column {
		return wrapJoin
	}
	return " "
}

// ParseAnswered reads every answered-questions block on a stripped pane, in
// screen order, each holding its questions and answers in the dialog's order.
func ParseAnswered(pane string) [][]ReviewAnswer {
	var (
		blocks  [][]ReviewAnswer
		block   []ReviewAnswer
		entry   strings.Builder
		reading bool
	)
	flushEntry := func() {
		text := strings.Join(strings.Fields(entry.String()), " ")
		entry.Reset()
		if text == "" {
			return
		}
		question, answer := text, ""
		for _, arrow := range []string{" → ", " →" + wrapJoin, wrapJoin + "→ ", wrapJoin + "→" + wrapJoin, "→ "} {
			if before, after, found := strings.Cut(text, arrow); found {
				question, answer = before, after
				break
			}
		}
		block = append(block, ReviewAnswer{Question: trimJoins(question), Answer: trimJoins(answer)})
	}
	flushBlock := func() {
		flushEntry()
		if reading {
			blocks = append(blocks, block)
		}
		block, reading = nil, false
	}
	lines := strings.Split(pane, "\n")
	width := paneWidth(lines)
	for i, line := range lines {
		text := strings.TrimSpace(line)
		joined := " "
		if i > 0 {
			joined = continuation(lines[i-1], line, width)
		}
		if strings.HasSuffix(text, answeredHeader) {
			flushBlock()
			reading = true
			continue
		}
		if !reading {
			continue
		}
		text = strings.TrimSpace(strings.TrimPrefix(text, "⎿"))
		switch {
		case text == "":
			flushBlock()
		case strings.HasPrefix(text, "· "):
			flushEntry()
			entry.WriteString(text[len("· "):])
		default:
			entry.WriteString(joined + text)
		}
	}
	flushBlock()
	return blocks
}

// SameText reports whether two renderings of one question or answer are the
// same words, whatever the pane's width did to their spacing and wrapping.
func SameText(a, b string) bool {
	return anyReading(a, b, func(a, b string) bool { return a == b })
}

// SameQuestion reports whether a question as one part of the screen draws it
// is the one another part draws: equal words, or one cut short of the other,
// because a dialog's prompt is read off its last few lines.
func SameQuestion(a, b string) bool {
	return anyReading(a, b, func(a, b string) bool {
		if a == "" || b == "" {
			return false
		}
		return a == b || strings.HasSuffix(a, b) || strings.HasSuffix(b, a)
	})
}

func trimJoins(text string) string { return strings.Trim(text, " "+wrapJoin) }

// Readable is text as the screen reads it once its wrapped rows are joined,
// for quoting back to a caller: a row that filled the pane is taken as
// broken inside a word.
func Readable(text string) string { return strings.ReplaceAll(text, wrapJoin, "") }

// anyReading holds when same holds for some reading of a and of b, each
// wrapJoin in them read as a space or as nothing.
func anyReading(a, b string, same func(a, b string) bool) bool {
	for _, x := range readings(normalise(a)) {
		for _, y := range readings(normalise(b)) {
			if same(x, y) {
				return true
			}
		}
	}
	return false
}

// readings is every way text's wrapJoins can be read. Past maxJoins it is
// only the two uniform ones, which keeps the count bounded; no answer this
// reads wraps a full row that often.
func readings(text string) []string {
	parts := strings.Split(text, wrapJoin)
	const maxJoins = 10
	if len(parts)-1 > maxJoins {
		return []string{strings.Join(parts, ""), strings.Join(parts, " ")}
	}
	out := []string{parts[0]}
	for _, part := range parts[1:] {
		next := make([]string, 0, 2*len(out))
		for _, head := range out {
			next = append(next, head+part, head+" "+part)
		}
		out = next
	}
	return out
}
