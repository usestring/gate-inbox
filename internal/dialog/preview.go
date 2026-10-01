package dialog

import (
	"regexp"
	"strings"
)

// Reading an AskUserQuestion whose options carry previews.
//
// When any option has a preview, Claude Code 2.1.286 draws the options in a
// column on the left and the focused option's preview in a box on the right,
// on the same rows (measured at 40, 50 and 60 columns). Read as an ordinary
// dialog, the box's text became part of every option's label, a label the
// column wrapped lost its second row, and at 40 and 50 columns the option's
// number is drawn at the start of that second row ("❯   Yes, push and open"
// over " 1.the PR"), so the option was no option at all. The preview's own
// text is clipped to the box; the whole of it is in the call, which
// Questions reads.

// previewBoxTop is the top edge of the preview box, drawn to the right of
// the first option.
var previewBoxTop = regexp.MustCompile(`\S.*?( +)┌─`)

// strayNumber is the second row of a wrapped option whose number was drawn
// there rather than on its first row.
var strayNumber = regexp.MustCompile(`^ ?(\d+)\.(\S.*)$`)

// unboxPreview is head with a preview box cut off its right and the option
// column it leaves read back into one line per option. A head with no
// preview box comes back as it is.
func unboxPreview(head string) string {
	lines := strings.Split(head, "\n")
	at, col := -1, 0
	for i, line := range lines {
		if m := previewBoxTop.FindStringIndex(line); m != nil {
			col = len([]rune(line[:m[1]])) - 2
			if boxBelow(lines[i+1:], col) {
				at = i
				break
			}
		}
	}
	if at < 0 {
		return head
	}
	var column []string
	for _, line := range lines[at:] {
		runes := []rune(line)
		if len(runes) > col {
			line = string(runes[:col])
		}
		column = append(column, strings.TrimRight(line, " \u00a0"))
	}
	var out []string
	for _, line := range column {
		last := len(out) - 1
		switch {
		case strayNumber.MatchString(line) && last >= 0 && !askOption.MatchString(out[last]):
			m := strayNumber.FindStringSubmatch(line)
			prev := out[last]
			marker := "  "
			if trimmed := strings.TrimLeft(prev, " \u00a0"); strings.HasPrefix(trimmed, "❯") {
				marker = "❯ "
				prev = strings.TrimPrefix(trimmed, "❯")
			}
			out[last] = marker + m[1] + ". " + strings.TrimSpace(prev) + " " + strings.TrimSpace(m[2])
		case strings.TrimSpace(line) != "" && last >= 0 && askOption.MatchString(out[last]) &&
			!askOption.MatchString(line) && indentOf(line) >= 4 && !isRule(line):
			out[last] += " " + strings.TrimSpace(line)
		default:
			out = append(out, line)
		}
	}
	return strings.Join(append(lines[:at:at], out...), "\n")
}

// boxBelow reports whether the rows under a box's top edge carry its side
// at col: the box is a box, not a corner glyph in someone's prose.
func boxBelow(lines []string, col int) bool {
	sides := 0
	for _, line := range lines {
		runes := []rune(line)
		if col < len(runes) && (runes[col] == '│' || runes[col] == '└') {
			sides++
			continue
		}
		break
	}
	return sides >= 2
}
