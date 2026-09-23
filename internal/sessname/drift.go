package sessname

import (
	"sort"
	"strings"
	"sync"
)

// Drift decides when an agent's own title has stopped describing what the
// session is doing.
//
// Claude Code writes its ai-title once and then leaves it: across 251
// transcripts on the machine this was built against, two ever changed, and
// both look like a person renaming by hand. So a session that opened on one
// task and spent the next six hours on another still carries the name of the
// first. The last-prompt record, by contrast, is rewritten every turn.
//
// The title is therefore the anchor and the prompts are the evidence against
// it, never the other way round. A prompt is a sentence somebody typed once;
// promoting it to a name the moment it stops matching would rename rows while
// the user is reading them, and a name that churns is worse than one that is a
// little stale. Everything below is the cost of being sure: three consecutive
// prompts with nothing of the title in them, at least two words the prompts
// agree on, and the same replacement derived twice in a row before anything is
// written.
type Drift struct {
	mu      sync.Mutex
	anchors map[string]string
	pending map[string]string
}

func NewDrift() *Drift {
	return &Drift{anchors: map[string]string{}, pending: map[string]string{}}
}

const (
	// driftPrompts is how many recent prompts must miss the title before it
	// is treated as stale.
	driftPrompts = 3
	// driftAgree is how many of those prompts a word must appear in to count
	// as what the session moved on to. One prompt mentioning something is an
	// aside; two is a subject.
	driftAgree = 2
	// promptHead bounds how much of a prompt is read. A pasted stack trace is
	// not what the session is about, and it is most of what was typed.
	promptHead = 240
)

// Title returns the text a session's name should be derived from: the agent's
// own title, or a replacement assembled from recent prompts once the title has
// demonstrably stopped applying. prompts are most recent last.
func (d *Drift) Title(id, title string, prompts []string) string {
	return d.title(id, title, prompts, true)
}

// TitleNow is Title for a rename somebody asked for by hand, and takes a
// replacement the first time it sees it.
//
// The confirmation across two passes exists so a ticker never renames a row on
// one off-topic turn. A keypress is not a ticker: making the operator press the
// key a second time to be given the name the first press had already worked out
// is the ticker's patience leaking into their hands.
func (d *Drift) TitleNow(id, title string, prompts []string) string {
	return d.title(id, title, prompts, false)
}

func (d *Drift) title(id, title string, prompts []string, confirm bool) string {
	if d == nil {
		return title
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	anchor := title
	if held, ok := d.anchors[id]; ok && held != "" {
		anchor = held
	}
	replacement := driftTitle(anchor, prompts)
	if replacement == "" {
		delete(d.pending, id)
		return anchor
	}
	// Confirmation across two passes is what keeps one off-topic turn from
	// reaching the rail; the pass interval is the manager's, not this
	// package's business.
	if confirm && d.pending[id] != replacement {
		d.pending[id] = replacement
		return anchor
	}
	delete(d.pending, id)
	d.anchors[id] = replacement
	return replacement
}

// Forget drops a session's drift state, for a row that has gone.
func (d *Drift) Forget(id string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.anchors, id)
	delete(d.pending, id)
}

// driftTitle returns the words recent prompts agree on when none of them
// touches the title, and the empty string in every other case.
func driftTitle(title string, prompts []string) string {
	if len(prompts) < driftPrompts {
		return ""
	}
	recent := prompts[len(prompts)-driftPrompts:]
	anchored := map[string]bool{}
	for _, word := range distinctive(title) {
		anchored[word] = true
	}
	if len(anchored) == 0 {
		return ""
	}
	seen := map[string]int{}
	var order []string
	for _, prompt := range recent {
		words := distinctive(head(prompt))
		for _, word := range words {
			if anchored[word] {
				return ""
			}
		}
		once := map[string]bool{}
		for _, word := range words {
			if once[word] {
				continue
			}
			once[word] = true
			if seen[word] == 0 {
				order = append(order, word)
			}
			seen[word]++
		}
	}
	var agreed []string
	for _, word := range order {
		if seen[word] >= driftAgree {
			agreed = append(agreed, word)
		}
	}
	if len(agreed) < 2 {
		return ""
	}
	sort.SliceStable(agreed, func(i, j int) bool { return seen[agreed[i]] > seen[agreed[j]] })
	if len(agreed) > maxWords {
		agreed = agreed[:maxWords]
	}
	return strings.Join(agreed, " ")
}

func head(text string) string {
	if len(text) <= promptHead {
		return text
	}
	return text[:promptHead]
}

// distinctive is the words of a text that could tell one session from another:
// no stopwords, no verbs of doing, nothing true of every row on the board.
func distinctive(text string) []string {
	var out []string
	for _, tok := range tokenize(text) {
		if stopwords[tok] || genericNouns[tok] || leadingVerbs[tok] || len(tok) < 3 {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// Keep drops the drift state of every session not listed, so a board that has
// been running for days is not remembering rows that closed on Tuesday.
func (d *Drift) Keep(ids []string) {
	if d == nil {
		return
	}
	live := make(map[string]bool, len(ids))
	for _, id := range ids {
		live[id] = true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for id := range d.anchors {
		if !live[id] {
			delete(d.anchors, id)
		}
	}
	for id := range d.pending {
		if !live[id] {
			delete(d.pending, id)
		}
	}
}
