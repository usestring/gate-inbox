// Package promptsnips turns the prompts an operator keeps retyping into
// suggestions for the composer.
//
// It is the pure half of the feature: given the user submissions read out of
// recent Claude and Codex transcripts, it counts how often each one recurs,
// keeps those seen at least MinOccurrences times, and ranks the survivors
// against whatever is in the input box. Reading transcripts and drawing the
// suggestions live elsewhere, so everything here is deterministic and cheap
// enough to run on every keystroke.
//
// A snippet is either a whole submission or one line of a multi-line
// submission. Lines count on their own because a recurring instruction is
// often pasted into otherwise different prompts, and only counting whole
// submissions would never see it.
//
// Counting is by submission, not by transcript row. A resumed or forked
// session copies earlier user turns into its new file, so the same submission
// can be read several times; every copy carries the original row's ID, and
// Build counts each ID once. Two submissions with the same text and different
// IDs are a genuine repeat and both count.
package promptsnips

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MinOccurrences is how many distinct submissions must contain a snippet
// before it is suggested: strictly more than three.
const MinOccurrences = 4

// Window bounds the history a snippet is counted over. Submissions older than
// this relative to Build's now are ignored, so a phrase that was habitual
// months ago stops being suggested once it stops being typed.
const Window = 30 * 24 * time.Hour

// MinLength and MaxLength bound a snippet's normalized length in runes. Below
// MinLength a suggestion saves less typing than reading it costs ("yes",
// "continue"); above MaxLength the text is a pasted document, not a phrase.
const (
	MinLength = 12
	MaxLength = 4000
)

// HalfLife is how quickly recency discounts frequency when ranking: a
// snippet last used HalfLife ago weighs half as much as one used now.
const HalfLife = 7 * 24 * time.Hour

// Submission is one user turn as its transcript recorded it.
type Submission struct {
	// ID identifies the turn across copies of the transcript. Empty means the
	// source has no stable ID, and the submission is counted as unique.
	ID   string
	Text string
	At   time.Time
}

// Snippet is a recurring piece of prompt text.
type Snippet struct {
	// Text is the most recent original spelling, which is what gets inserted.
	Text string
	// Key is Text normalized; snippets are counted and matched on it.
	Key   string
	Count int
	Last  time.Time
}

// Normalize is the form snippets are counted and matched in: case folded,
// runs of whitespace collapsed to one space, and trimmed.
func Normalize(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}

// Build counts the submissions and returns every eligible snippet, most
// frequent first. Submissions with a zero At are kept: an unknown time is not
// evidence of age.
func Build(subs []Submission, now time.Time) []Snippet {
	cutoff := now.Add(-Window)
	seen := make(map[string]bool, len(subs))
	counts := make(map[string]*Snippet)
	for _, sub := range subs {
		if !sub.At.IsZero() && sub.At.Before(cutoff) {
			continue
		}
		if sub.ID != "" {
			if seen[sub.ID] {
				continue
			}
			seen[sub.ID] = true
		}
		for key, text := range units(sub.Text) {
			snip := counts[key]
			if snip == nil {
				snip = &Snippet{Key: key}
				counts[key] = snip
			}
			snip.Count++
			if snip.Text == "" || !sub.At.Before(snip.Last) {
				snip.Text, snip.Last = text, sub.At
			}
		}
	}
	var out []Snippet
	for _, snip := range counts {
		if snip.Count >= MinOccurrences {
			out = append(out, *snip)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// units returns the candidate snippets in one submission, keyed by normalized
// text, so a line repeated within one submission still counts once.
func units(text string) map[string]string {
	out := make(map[string]string)
	add := func(s string) {
		s = strings.TrimSpace(s)
		key := Normalize(s)
		if n := utf8.RuneCountInString(key); n >= MinLength && n <= MaxLength {
			out[key] = s
		}
	}
	add(text)
	if lines := strings.Split(text, "\n"); len(lines) > 1 {
		for _, line := range lines {
			add(line)
		}
	}
	return out
}

// Relevance is how well a snippet matches the input, best first.
type Relevance int

const (
	NoMatch Relevance = iota
	// TokenMatch: every input word begins some word of the snippet.
	TokenMatch
	// WordMatch: the input appears in the snippet starting at a word.
	WordMatch
	// PrefixMatch: the snippet starts with the input.
	PrefixMatch
)

// Match reports how the normalized input matches a snippet key. An input the
// snippet equals is NoMatch: it has already been typed.
func Match(key, input string) Relevance {
	if input == "" || key == input {
		return NoMatch
	}
	if strings.HasPrefix(key, input) {
		return PrefixMatch
	}
	for i := strings.Index(key, input); i >= 0; {
		if r, _ := utf8.DecodeLastRuneInString(key[:i]); !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return WordMatch
		}
		next := strings.Index(key[i+1:], input)
		if next < 0 {
			break
		}
		i += 1 + next
	}
	words := strings.Fields(key)
	for _, want := range strings.Fields(input) {
		found := false
		for _, word := range words {
			if strings.HasPrefix(word, want) {
				found = true
				break
			}
		}
		if !found {
			return NoMatch
		}
	}
	return TokenMatch
}

// Weight is a snippet's frequency discounted by how long ago it was last used.
func Weight(snip Snippet, now time.Time) float64 {
	if snip.Last.IsZero() {
		return float64(snip.Count)
	}
	age := max(0, now.Sub(snip.Last))
	return float64(snip.Count) * math.Exp2(-float64(age)/float64(HalfLife))
}

// Suggest returns up to limit snippets matching the input, ranked by
// relevance, then Weight, then recency. Input shorter than two runes once
// normalized suggests nothing: one character matches too much to be useful.
func Suggest(snips []Snippet, input string, now time.Time, limit int) []Snippet {
	input = Normalize(input)
	if utf8.RuneCountInString(input) < 2 || limit <= 0 {
		return nil
	}
	type ranked struct {
		snip   Snippet
		rel    Relevance
		weight float64
	}
	var hits []ranked
	for _, snip := range snips {
		if rel := Match(snip.Key, input); rel != NoMatch {
			hits = append(hits, ranked{snip, rel, Weight(snip, now)})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.rel != b.rel {
			return a.rel > b.rel
		}
		if a.weight != b.weight {
			return a.weight > b.weight
		}
		if !a.snip.Last.Equal(b.snip.Last) {
			return a.snip.Last.After(b.snip.Last)
		}
		return a.snip.Key < b.snip.Key
	})
	out := make([]Snippet, 0, min(limit, len(hits)))
	for _, hit := range hits[:min(limit, len(hits))] {
		out = append(out, hit.snip)
	}
	return out
}
