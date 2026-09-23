package status

import (
	"regexp"
	"regexp/syntax"
	"strings"
)

// A matcher is one configured pattern plus a plain string every match of it
// must contain.
//
// The rules run about ten scans over the same pane text per session per pass,
// and at 55 sessions that was ~49ms of regex every two seconds -- on panes
// where almost every rule misses, because an ordinary working pane has no
// dialog in it at all. strings.Contains over the same 12.5 KB reads at
// 2,470 MB/s, some forty times faster than the quickest of those patterns, so
// asking the cheap question first turns a miss into 0.2µs and leaves a hit
// costing what it always did.
//
// The literal is derived from the pattern rather than written beside it,
// because the patterns are the user's: a tool's rules come out of the config
// file and a literal maintained by hand would go stale the first time
// somebody edited one. A pattern with no literal that is certainly required
// -- an alternation, a character class, anything case-folded -- gets no
// prefilter and runs exactly as before. That asymmetry is the safety
// argument: a wrong literal silently stops a status from ever matching, and a
// missing one only costs time.
type matcher struct {
	re  *regexp.Regexp
	lit string
}

func newMatcher(pattern string) (*matcher, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return &matcher{re: re, lit: requiredLiteral(pattern)}, nil
}

// String is the pattern itself, so a failure message names what did not
// match rather than a pointer.
func (m *matcher) String() string { return m.re.String() }

func (m *matcher) MatchString(s string) bool {
	if m.lit != "" && !strings.Contains(s, m.lit) {
		return false
	}
	return m.re.MatchString(s)
}

func (m *matcher) FindStringIndex(s string) []int {
	if m.lit != "" && !strings.Contains(s, m.lit) {
		return nil
	}
	return m.re.FindStringIndex(s)
}

func (m *matcher) FindAllStringIndex(s string, n int) [][]int {
	if m.lit != "" && !strings.Contains(s, m.lit) {
		return nil
	}
	return m.re.FindAllStringIndex(s, n)
}

// requiredLiteral returns a string that appears verbatim in every string the
// pattern matches, or "" when the pattern implies none.
//
// It is deliberately timid. Every operator it does not understand answers ""
// rather than a guess, and the only thing it ever returns is a run of literal
// runes the match cannot avoid passing through.
func requiredLiteral(pattern string) string {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return ""
	}
	return longestRequired(parsed.Simplify())
}

func longestRequired(re *syntax.Regexp) string {
	switch re.Op {
	case syntax.OpLiteral:
		// A case-folded literal is not a literal: "ERROR:" matches the
		// pattern and contains none of "error:". Nothing is extracted from
		// one at all, rather than the caseless runes inside it, because a
		// literal worth gating on is one long enough to be rare.
		if re.Flags&syntax.FoldCase != 0 {
			return ""
		}
		return string(re.Rune)
	case syntax.OpCapture, syntax.OpPlus:
		// One repetition at minimum, so whatever the body requires, the whole
		// requires.
		return longestRequired(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return longestRequired(re.Sub[0])
		}
		return ""
	case syntax.OpConcat:
		// Every piece of a concatenation is passed through, so any piece's
		// requirement is the whole's. The longest is taken because a longer
		// literal is the rarer one, and rarity is the entire point.
		best := ""
		for _, sub := range re.Sub {
			if lit := longestRequired(sub); len(lit) > len(best) {
				best = lit
			}
		}
		return best
	}
	// Alternations, character classes, optional groups, anchors and anything
	// else: a match can reach the end without any particular byte. An
	// alternation whose branches share a literal would be extractable in
	// principle; it is not extracted here, because "in principle" is how a
	// status stops matching.
	return ""
}
