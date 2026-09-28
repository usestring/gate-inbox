package dialog

import "strings"

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
		question, answer, found := strings.Cut(text, " → ")
		if !found {
			question, answer, _ = strings.Cut(text, "→ ")
		}
		block = append(block, ReviewAnswer{Question: strings.TrimSpace(question), Answer: strings.TrimSpace(answer)})
	}
	flushBlock := func() {
		flushEntry()
		if reading {
			blocks = append(blocks, block)
		}
		block, reading = nil, false
	}
	for _, line := range strings.Split(pane, "\n") {
		text := strings.TrimSpace(line)
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
			entry.WriteString(" " + text)
		}
	}
	flushBlock()
	return blocks
}

// SameText reports whether two renderings of one question or answer are the
// same words, whatever the pane's width did to their spacing and wrapping.
func SameText(a, b string) bool { return normalise(a) == normalise(b) }

// SameQuestion reports whether a question as one part of the screen draws it
// is the one another part draws: equal words, or one cut short of the other,
// because a dialog's prompt is read off its last few lines.
func SameQuestion(a, b string) bool {
	a, b = normalise(a), normalise(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasSuffix(a, b) || strings.HasSuffix(b, a)
}
