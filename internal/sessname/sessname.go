// Package sessname turns the prose title an agent CLI keeps for a
// conversation into a name narrow enough for the rail.
//
// The titles are written by a frontier model with the whole conversation in
// front of it, so they are good; they are also sentences -- "Build UK business
// rates overpayment detection system" -- and the rail has about forty columns.
// Everything here is the squeeze, done locally with word lists rather than a
// model: naming eighty-seven rows is a thing that has to happen on a ticker, in
// a terminal, with no network and no spend.
//
// The property being optimised is not prettiness. Eighty-seven rows all reading
// "sample-repo" is the problem being solved, so two rows reading "http-gateway-fix" is the
// same problem in a nicer font. Compress returns candidates rather than a name,
// shortest first, and the caller walks them until one is free -- a collision is
// broken by spending more of the title, never by a counter.
package sessname

import (
	"sort"
	"strings"
	"unicode"
)

// Compressor turns a title into rail names, shortest first.
//
// The interface exists so a model-backed compressor can be dropped in without
// the assignment and drift rules moving. Nothing here calls one.
type Compressor interface {
	Compress(title string) []string
}

// maxWords bounds how much title a collision may spend. Past five words the
// name has stopped being scannable, which is the only reason it is short.
const maxWords = 5

// maxLen is the widest name the rail shows without truncating it.
const maxLen = 36

// Kebab is the deterministic compressor: strip the leading verb and the
// stopwords, keep the distinctive nouns in the order the title had them.
type Kebab struct{}

// Compress returns candidate names for title, shortest first.
//
// The first candidate is three content words, or all of them when there are
// fewer, which is what makes "Investigate and reduce disk usage" come out as
// disk-usage rather than padded. Each later candidate spends one more word.
// The last one puts the dropped leading verb back on the end, which reads as a
// noun there -- clawed-extractor-port -- and is a real distinction when two
// titles share their subject and differ only in what is being done to it.
func (Kebab) Compress(title string) []string {
	words, verb := content(title)
	if len(words) == 0 {
		return nil
	}
	start := min(3, len(words))
	var out []string
	seen := map[string]bool{}
	add := func(parts []string) {
		name := strings.Join(parts, "-")
		if name == "" || len(name) > maxLen || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for n := start; n <= min(maxWords, len(words)); n++ {
		add(words[:n])
	}
	if verb != "" {
		for n := start; n <= min(maxWords-1, len(words)); n++ {
			add(append(append([]string{}, words[:n]...), verb))
		}
	}
	return out
}

// content splits a title into the words worth keeping, and returns the leading
// verb separately so a caller can spend it later.
func content(title string) ([]string, string) {
	tokens := tokenize(title)
	verb := ""
	// Only a run at the front is dropped: "and" and "then" join two of them
	// ("Investigate and reduce disk usage"), and a verb further in is usually
	// carrying the meaning ("feed builder empty source gate").
	for len(tokens) > 0 {
		head := tokens[0]
		if leadingVerbs[head] {
			if verb == "" {
				verb = head
			}
			tokens = tokens[1:]
			continue
		}
		if verb != "" && joiners[head] {
			tokens = tokens[1:]
			continue
		}
		break
	}
	var kept, generic []string
	for _, tok := range tokens {
		switch {
		case stopwords[tok]:
		case genericNouns[tok]:
			generic = append(generic, tok)
		default:
			kept = append(kept, tok)
		}
	}
	// A generic noun is dropped for being uninformative, not for being wrong.
	// A title made entirely of them still has to name its row.
	if len(kept) < 2 {
		kept = append(kept, generic...)
	}
	if len(kept) == 0 && verb != "" {
		kept = []string{verb}
		verb = ""
	}
	return kept, verb
}

// tokenize lowercases a title and splits it into words.
//
// Inner hyphens, dots, slashes and underscores survive as hyphens, because the
// tokens they hold together are the most distinctive things a title has:
// abc-135518, x-gw-auth, api.example.com. A parenthetical is dropped whole --
// opencode ends every subagent title with "(@general subagent)", which would
// otherwise be the three words every one of them shares.
func tokenize(title string) []string {
	title = dropParentheticals(title)
	var tokens []string
	var word strings.Builder
	flush := func() {
		tok := strings.Trim(word.String(), "-")
		word.Reset()
		if tok == "" {
			return
		}
		tokens = append(tokens, tok)
	}
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == '/':
			word.WriteRune('-')
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func dropParentheticals(title string) string {
	var out strings.Builder
	depth := 0
	for _, r := range title {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

// Entry is one session asking for a name.
type Entry struct {
	ID string
	// Title is the agent's own title for the conversation.
	Title string
	// Current is the name the row has now, kept when the title still yields
	// it so a refresh that changes nothing writes nothing.
	Current string
	// Fallback names the row when the title compresses to nothing, and is
	// what disambiguates two sessions whose titles are identical. The cwd
	// basename is what the caller has.
	Fallback string
}

// Assign hands every entry a name no other row is using.
//
// taken is every name on the board, including the names of the rows being
// assigned and of rows this pass has nothing to say about. Passing the whole
// board rather than only the untouched part of it is what protects a row that
// could not be named this time: its name stays reserved instead of being handed
// to somebody else while it goes on wearing it.
//
// Order is by id rather than by arrival so the same board names the same way
// twice, and an entry keeping the name it already has is settled first: a row
// the user is looking at should not lose its name to a row that turned up
// later and compressed to the same thing.
func Assign(c Compressor, entries []Entry, taken map[string]bool) map[string]string {
	used := map[string]bool{}
	for name := range taken {
		used[name] = true
	}
	// A row's own name is not an obstacle to itself. Without this an entry
	// already called what its title says would fail to keep it and be moved to
	// the next candidate, every pass, for ever.
	for _, entry := range entries {
		delete(used, entry.Current)
	}
	sorted := append([]Entry{}, entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	out := make(map[string]string, len(sorted))
	pending := make([]Entry, 0, len(sorted))
	for _, entry := range sorted {
		candidates := c.Compress(entry.Title)
		if entry.Current != "" && !used[entry.Current] {
			for _, name := range candidates {
				if name == entry.Current {
					out[entry.ID] = entry.Current
					used[entry.Current] = true
					break
				}
			}
		}
		if _, ok := out[entry.ID]; !ok {
			pending = append(pending, entry)
		}
	}
	for _, entry := range pending {
		name := pick(c.Compress(entry.Title), entry.Fallback, used)
		if name == "" {
			continue
		}
		out[entry.ID] = name
		used[name] = true
	}
	return out
}

// pick walks the candidates for a free name and only then falls back.
//
// Two conversations really can carry the same title -- opencode spawns four
// subagents on one prompt and titles them alike -- and at that point there is
// no more title to spend, so the counter is the honest answer rather than a
// shortcut past one.
func pick(candidates []string, fallback string, used map[string]bool) string {
	for _, name := range candidates {
		if !used[name] {
			return name
		}
	}
	base := ""
	if len(candidates) > 0 {
		base = candidates[len(candidates)-1]
	} else if fallback != "" {
		base = strings.Join(tokenize(fallback), "-")
	}
	if base == "" {
		return ""
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		name := base + "-" + itoa(i)
		if !used[name] {
			return name
		}
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
