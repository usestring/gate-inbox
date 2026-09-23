package handover

import (
	"regexp"
	"strings"
)

// A ledger, filtered for the restart prompt.
//
// The ledger on disk is the run's memory and the operator's record; nothing
// here rewrites it. What gets filtered is the copy fenced into a fresh
// worker's prompt, because that copy is where pollution does its damage: a
// leg that stopped on a decline writes an essay under a section of its own
// invention, and every replacement after it reads the essay, anchors on it,
// and writes its own. Established and Refuted -- the append-only sections
// the whole run depends on -- are never touched, and neither are Open or
// Next: entries there are status, and the manager and operator work through
// them.
//
// A stub is a marker, not a deletion. It says a leg stopped here on a
// decline, which is the information a replacement needs; the essay is what
// it needs to be spared.

// declineish is the weaker signal a ledger entry is a decline essay. A
// transcript turn is read in the worker's own voice and decline.LooksLike is
// enough; a ledger entry is often written about the decline, in the third
// person, so the words that matter are the vocabulary around it.
var declineish = regexp.MustCompile(`(?i)\b(declin\w*|refus\w*|halted?|will not (?:do|act|advance|proceed))\b`)

// resolved is the signal an entry records an answer rather than an essay:
// it states a resolution in its own voice -- "is answered", "superseded
// by", "the operator supplied" -- and such an entry is kept even when it
// names the decline, because it is the replacement's instruction, not its
// anchor. A bare mention is not a signal: an essay that opens "after
// reading the operator's Resolved entry" is exactly the anchor this filter
// exists to remove, and the word Resolved appears in it only as a pointer.
var resolved = regexp.MustCompile(`(?i)\b((?:is|was|are|has been|now|already)\s+(?:answered|resolved|superseded|withdrawn)|answered, not withdrawn|superseded by|operator'?s? (?:supplied|answered|removed|decided|approved))\b`)

// declineStubEntry is the one-line marker that replaces a decline essay.
const declineStubEntry = "- [filtered for handover: a leg of this run stopped here on a decline; " +
	"the operator's answer, if any, is in steering.md]"

// Ledger filters the ledger text and reports what it removed. changed says
// whether anything was stubbed at all, so a clean ledger filters to itself.
func Ledger(text string) (filtered string, stats Stats, changed bool) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	out := make([]string, 0, len(lines))
	inContract := true
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if heading, isHeading := ledgerHeading(line); isHeading {
			inContract = contractSection(heading)
			out = append(out, line)
			continue
		}
		if inContract || !startsEntry(line) {
			out = append(out, line)
			continue
		}
		// One entry: its bullet line plus the wrap that follows, up to the
		// next entry, heading or blank line.
		entry := line
		for i+1 < len(lines) && lines[i+1] != "" && !startsEntry(lines[i+1]) {
			_, isHeading := ledgerHeading(lines[i+1])
			if isHeading {
				break
			}
			i++
			entry += "\n" + lines[i]
		}
		if declineish.MatchString(entry) && !resolved.MatchString(entry) {
			stats.Stubbed++
			out = append(out, declineStubEntry)
			continue
		}
		out = append(out, entry)
	}
	if stats.Stubbed == 0 {
		return text, stats, false
	}
	return strings.Join(out, "\n") + "\n", stats, true
}

// ledgerHeading reads a heading line, reporting its text. Any H2 a worker
// invented -- "## Halted", "## Resolved", "## State of play" -- is a
// non-contract section and gets its entries filtered.
func ledgerHeading(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "## ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), true
}

// contractSection reports whether a heading names one of the ledger's four
// contract sections, which a filter never enters.
func contractSection(heading string) bool {
	switch strings.ToLower(strings.TrimSpace(heading)) {
	case "established", "refuted", "open", "next":
		return true
	}
	return false
}

// startsEntry reports whether a line opens a list entry.
func startsEntry(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") ||
		startsNumbered(line)
}

func startsNumbered(line string) bool {
	for i, r := range line {
		if r >= '0' && r <= '9' {
			continue
		}
		return i > 0 && (r == '.' || r == ')') && i < 5
	}
	return false
}
