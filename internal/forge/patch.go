package forge

import (
	"strconv"
	"strings"
)

// DiffKind is what one line of a patch is.
type DiffKind int

const (
	DiffContext DiffKind = iota
	DiffAdd
	DiffDelete
	DiffHunk
	// DiffNote is git's "\ No newline at end of file": it belongs to the line before it and has
	// no number of its own.
	DiffNote
)

// DiffLine is one line of a patch with the line numbers it has on each side. Old is zero on an
// added line and New on a removed one, as on GitHub's own gutter; a hunk header and a note have
// neither.
type DiffLine struct {
	Kind DiffKind
	Old  int
	New  int
	Text string
}

// ParsePatch numbers each line of a unified-diff patch, the form GitHub serves per file. The
// numbers are what a review thread's line and side point at, so they come from the hunk headers
// rather than from counting from the top. A line before the first hunk header, or a header that
// does not parse, keeps the numbering it had.
func ParsePatch(patch string) []DiffLine {
	if patch == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	out := make([]DiffLine, 0, len(lines))
	old, cur := 0, 0
	for _, text := range lines {
		if strings.HasPrefix(text, "@@") {
			if o, n, ok := hunkStarts(text); ok {
				old, cur = o, n
			}
			out = append(out, DiffLine{Kind: DiffHunk, Text: text})
			continue
		}
		if text == "" {
			// GitHub trims the space off an empty context line.
			text = " "
		}
		body := text[1:]
		switch text[0] {
		case '+':
			out = append(out, DiffLine{Kind: DiffAdd, New: cur, Text: body})
			cur++
		case '-':
			out = append(out, DiffLine{Kind: DiffDelete, Old: old, Text: body})
			old++
		case '\\':
			out = append(out, DiffLine{Kind: DiffNote, Text: text})
		default:
			out = append(out, DiffLine{Kind: DiffContext, Old: old, New: cur, Text: body})
			old++
			cur++
		}
	}
	return out
}

// hunkStarts reads the first line of each side from "@@ -a,b +c,d @@ heading".
func hunkStarts(header string) (old, cur int, ok bool) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, false
	}
	start := func(field string) (int, bool) {
		head, _, _ := strings.Cut(field[1:], ",")
		n, err := strconv.Atoi(head)
		return n, err == nil
	}
	o, okOld := start(fields[1])
	n, okNew := start(fields[2])
	return o, n, okOld && okNew
}
