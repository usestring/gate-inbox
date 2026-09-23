// Package search is the history half of the board filter: what every session
// on the board has said and done, held so a query can find the session that
// mentioned a hostname three hours ago as easily as the one showing it on
// screen now.
//
// Scope is the board, not the disk. The index holds one source per board row
// that has a conversation, and nothing else, so its size follows the number
// of sessions rather than the age of the machine. A row that leaves the board
// leaves the index on the next pass.
//
// The index is the sessions' searchable text in memory, case-folded once,
// and a query is a substring scan over it. That is the shape the numbers
// chose: on the operator's board — 55 sessions, 160MB of transcripts, 21MB
// of searchable text once tool results are capped — an FTS5 trigram index
// took 13s to build and 44MB on disk and answered in 5–17ms, while a scan of
// the same text answers in the same band and costs nothing to build beyond
// parsing the files (reallayout_bench_test.go keeps the comparison runnable).
// Real transcripts are code, paths and hashes, and their trigram cardinality
// is what the FTS index paid for; a scan pays per byte and nothing else.
//
// The transcripts are append-only files, so a refresh reads only what was
// appended since the last one, under a byte budget per pass. Steady state is
// one stat per source; a cold start over a fleet of long sessions is a run of
// budgeted passes that never blocks the UI, which searches metadata alone
// until the history catches up. Nothing persists: a restart rebuilds from the
// files in a couple of seconds.
package search

import (
	"bytes"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/usestring/gate-inbox/internal/tracing"
)

// MinQueryRunes is the shortest query the index answers, counted without the
// wildcard. Shorter queries match nearly everything and are left to the
// metadata filter, as they always were.
const MinQueryRunes = 3

// Wildcard stands for any run of characters in a query, in the index and in
// the board's own metadata and pane match alike, so "db-*-07" means one thing
// wherever it is typed.
const Wildcard = "*"

// DefaultSessionBytes bounds one session's searchable text; a session that
// grows past it keeps its newest turns. DefaultTotalBytes bounds the index,
// and past it the largest session gives up its oldest turns first. On the
// operator's board the whole searchable text of 55 sessions is 29MB and the
// largest session is under 4MB, so neither bound is reached in ordinary use;
// they exist so a runaway session or a huge board cannot grow the manager
// without limit.
const (
	DefaultSessionBytes = 64 << 20
	DefaultTotalBytes   = 512 << 20
)

// Options tunes an index. The zero value is the production shape.
type Options struct {
	// OpenCodeDB is opencode's own sqlite database, opened read-only when a
	// target names an opencode conversation. Empty disables opencode.
	OpenCodeDB string
	// Limits caps what one transcript line contributes; zero takes DefaultLimits.
	Limits Limits
	// SessionBytes caps one session's text; zero takes DefaultSessionBytes.
	SessionBytes int
	// TotalBytes caps the index; zero takes DefaultTotalBytes.
	TotalBytes int
}

// Index is the searchable text of the board's sessions. Refresh is for one
// goroutine at a time; Search may run concurrently with it from any goroutine.
type Index struct {
	limits       Limits
	sessionBytes int
	totalBytes   int

	opencodePath string
	opencode     opencodeReader

	// mu guards sessions: Refresh swaps and appends under the write lock,
	// briefly, after parsing outside it; Search holds the read lock for the
	// duration of one scan.
	mu       sync.RWMutex
	sessions map[string]*session
	total    int
	// hist counts every byte the index holds. A query anchors its scan on
	// the needle's rarest byte, so a word starting with a common letter is
	// not tested at every such letter in the text.
	hist [256]int64

	// last is the previous query and its hits. A keystroke usually extends
	// the query, and a session that lacks the shorter query cannot hold the
	// longer one, so the next scan covers the hits alone. Refresh drops it:
	// new text can make a fresh hit anywhere.
	lastMu sync.Mutex
	last   struct {
		query string
		hits  map[string]bool
	}
}

// session is one source: what the index remembers about the file, and the
// text it has read from it, lowercased, one turn per line.
// stamp is what a refresh pass compares this session's file against.
func (s *session) stamp() stamp {
	return stamp{size: s.size, mtime: s.mtime, tail: s.tail, tailRead: s.tailRead}
}

type session struct {
	tool     string
	path     string
	size     int64
	mtime    int64
	tail     uint64
	tailRead bool
	indexed  int64
	cursor   opencodeCursor
	text     []byte
	turns    int
	// created is one line per pull request URL the session watched itself
	// open: a tool call that ran the PR-creating command, paired by id with
	// the result that printed the URL. Kept for the life of the entry rather
	// than trimmed with the text: a pull request opened three hours ago is
	// still this session's work. opening holds the ids of creating calls
	// whose result has not arrived yet. A false value identifies a poll,
	// whose output needs the wrapper's confirmation to establish creation.
	created []byte
	opening map[string]bool
	// blooms holds one trigram filter per blockBytes of text, so a query
	// skips every block that cannot contain it without reading the block.
	blooms []bloom
	// newlines[i] is how many turns end before byte i*newlineStep, so a
	// hit's distance from the newest turn is one lookup and a scan of at
	// most newlineStep bytes rather than a count to the end of the text.
	newlines []int32
}

// New creates an empty index.
func New(opts Options) *Index {
	x := &Index{limits: opts.Limits, sessionBytes: opts.SessionBytes, totalBytes: opts.TotalBytes,
		opencodePath: opts.OpenCodeDB, sessions: map[string]*session{}}
	if x.limits == (Limits{}) {
		x.limits = DefaultLimits
	}
	if x.sessionBytes <= 0 {
		x.sessionBytes = DefaultSessionBytes
	}
	if x.totalBytes <= 0 {
		x.totalBytes = DefaultTotalBytes
	}
	return x
}

// Created is every pull request URL a session's history shows it opening
// itself, one per line, oldest first. Empty for a session the index does not
// hold or has not caught up on yet. Safe to call from any goroutine.
//
// Opening is the evidence, not mentioning: a session that discusses ten pull
// requests is working on none of them in particular, and a board that linked
// all ten would be wrong about nine. The pairing of call and result is what
// says the URL came back from the command that made it.
func (x *Index) Created(key string) string {
	x.mu.RLock()
	defer x.mu.RUnlock()
	s := x.sessions[key]
	if s == nil {
		return ""
	}
	return string(s.created)
}

// Close releases the opencode connection, if one was opened.
func (x *Index) Close() error {
	return x.opencode.close()
}

// Hit is one session the query was found in, and how strongly.
type Hit struct {
	Key string
	// Count is how many times the query occurs in the session's text, up to
	// maxHits, newest occurrences first when the cap bites.
	Count int
	// Score weights each occurrence by what wrote it and how recent it is:
	// one the operator typed in the newest turn counts a full point, the
	// same term in a tool's output a third of that, and either about half
	// as much recencyTurns turns back. A session that keeps talking about
	// the query outranks one that mentioned it once, a recent mention
	// outranks an old one, and what the operator said outranks what a tool
	// printed.
	Score float64
}

const (
	// maxHitsPerBlock bounds what one block reports: a block is a few hundred
	// turns, and sixteen mentions in it already say "chatty" as loudly as
	// two hundred would, at a fraction of the scan.
	maxHitsPerBlock = 16
	recencyTurns    = 20.0
)

// Search is every board session whose history contains query, with
// Wildcard standing for any run of characters, best first. ok is false when
// the query is too short for the index, which is not an error: the caller
// matches metadata alone, as it would with no index at all.
//
// The text was lowercased when the index read it and the query is lowercased
// here, the same fold the board applies to pane text. Sessions are scanned
// in parallel: the work is a substring search per session, and the sessions
// are independent.
func (x *Index) Search(query string, limit int) (hits []Hit, ok bool, err error) {
	// The operator is waiting on this one with their hands on the keyboard,
	// which is the whole reason it is a scan of memory rather than an index.
	// What the span may say about the query is how long it was, never what it
	// said: a filter is typed, and typing is the one thing that must not leave
	// this machine.
	scanned := 0
	if tracing.Enabled() {
		started := time.Now()
		defer func() {
			tracing.Record("search.query", started, time.Now(), err,
				tracing.Attr{Key: "query.runes", Value: utf8.RuneCountInString(query)},
				tracing.Attr{Key: "scanned", Value: scanned},
				tracing.Attr{Key: "hits", Value: len(hits)},
				tracing.Attr{Key: "answered", Value: ok})
		}()
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if utf8.RuneCountInString(strings.ReplaceAll(query, Wildcard, "")) < MinQueryRunes {
		return nil, false, nil
	}
	x.mu.RLock()
	defer x.mu.RUnlock()
	parts := x.splitQuery(query)

	// Narrowing: when the last query is a literal substring of this one,
	// only its hits can hold this one.
	candidates := x.sessions
	x.lastMu.Lock()
	if x.last.hits != nil && x.last.query != "" && !strings.Contains(x.last.query, Wildcard) && strings.Contains(query, x.last.query) {
		candidates = make(map[string]*session, len(x.last.hits))
		for key := range x.last.hits {
			if s, ok := x.sessions[key]; ok {
				candidates[key] = s
			}
		}
	}
	x.lastMu.Unlock()
	// After the narrowing, because narrowing is the optimisation and the
	// number worth reporting is what this query actually had to read.
	scanned = len(candidates)

	// Work is handed out in chunks rather than sessions so one long session
	// does not bound the query on a single core. A plain query may be split
	// at any point as long as the pieces overlap by a needle's length; a
	// wildcard query's segments must be found in order across the whole
	// session, so it scans sessions whole. Each worker reports where it
	// found the query, and the positions score the session afterwards.
	type job struct {
		key  string
		text []byte
		base int
		// span is how much of text a match may start in: the block itself,
		// not the overlap the next job also covers.
		span int
	}
	jobs := make(chan job)
	positions := map[string][]int{}
	var found sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < max(1, runtime.GOMAXPROCS(0)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if at := occurrences(j.text, parts, j.span, j.base); len(at) > 0 {
					found.Lock()
					positions[j.key] = append(positions[j.key], at...)
					found.Unlock()
				}
			}
		}()
	}
	overlap := len(parts[0].needle) - 1
	for key, s := range candidates {
		text := s.text
		if len(parts) > 1 {
			// Every segment must appear somewhere in the session; ordering
			// is the scan's job.
			if s.mayHoldAll(parts) {
				jobs <- job{key, text, 0, len(text)}
			}
			continue
		}
		for i, at := 0, 0; at < len(text); i, at = i+1, at+blockBytes {
			if s.blooms[i].mayHold(parts[0]) {
				end := min(len(text), at+blockBytes+overlap)
				jobs <- job{key, text[at:end], at, min(blockBytes, len(text)-at)}
			}
		}
	}
	close(jobs)
	wg.Wait()

	keys := make(map[string]bool, len(positions))
	for key, at := range positions {
		keys[key] = true
		hits = append(hits, x.sessions[key].score(key, at))
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Key < hits[j].Key
	})
	// Search calls can overlap; lastMu keeps a (query, hits) pair whole. A
	// Refresh cannot overlap: it clears the cache under the write lock.
	x.lastMu.Lock()
	x.last.query, x.last.hits = query, keys
	x.lastMu.Unlock()
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, true, nil
}

// occurrences is where the query starts in text, as absolute offsets, for
// starts inside the first span bytes. A single-segment query is found
// directly; a wildcard query is found segment by segment in order, and its
// occurrence is where its first segment starts.
func occurrences(text []byte, parts []needle, span, base int) []int {
	var at []int
	for from := 0; from < span && len(at) < maxHitsPerBlock; {
		start := indexRare(text[from:], parts[0])
		if start < 0 || from+start >= span {
			break
		}
		start += from
		pos := start + len(parts[0].needle)
		matched := true
		for _, part := range parts[1:] {
			idx := indexRare(text[pos:], part)
			if idx < 0 {
				matched = false
				break
			}
			pos += idx + len(part.needle)
		}
		if !matched {
			break
		}
		at = append(at, base+start)
		from = start + 1
	}
	return at
}

// score is a session's rank for its occurrences: each one weighted by how
// many turns back from the session's newest text it sits.
func (s *session) score(key string, at []int) Hit {
	hit := Hit{Key: key, Count: len(at)}
	for _, pos := range at {
		ago := s.turns - s.turnsBefore(pos)
		hit.Score += s.kindAt(pos).Weight() / (1 + float64(ago)/recencyTurns)
	}
	return hit
}

// kindAt is what wrote the line an offset falls in: the byte the line
// starts with, continuation bit aside.
func (s *session) kindAt(pos int) Kind {
	start := bytes.LastIndexByte(s.text[:pos], '\n') + 1
	if start >= len(s.text) {
		return KindAssistant
	}
	return Kind(s.text[start] &^ continuation)
}

// turnsBefore is how many turns end before byte offset pos.
func (s *session) turnsBefore(pos int) int {
	seg := pos / newlineStep
	if seg >= len(s.newlines) {
		seg = len(s.newlines) - 1
	}
	if seg < 0 {
		return 0
	}
	return int(s.newlines[seg]) + turnStarts(s.text, seg*newlineStep, pos)
}

// Match reports whether text contains query, with Wildcard standing for any
// run of characters. The board applies it to what a row prints and shows so
// a query means the same thing there as in the index. Both arguments are
// matched as given; the caller folds case.
func Match(text, query string) bool {
	at := 0
	for _, part := range strings.Split(query, Wildcard) {
		idx := strings.Index(text[at:], part)
		if idx < 0 {
			return false
		}
		at += idx + len(part)
	}
	return true
}

// needle is one wildcard-free segment of a query: its bytes, the offset of
// its rarest byte (chosen against the index's histogram once per query),
// and the bloom bits of its leading trigrams.
type needle struct {
	needle []byte
	anchor int
	bits   []uint32
}

func (x *Index) splitQuery(query string) []needle {
	var parts []needle
	for _, part := range strings.Split(query, Wildcard) {
		n := needle{needle: []byte(part)}
		for i, b := range n.needle {
			if x.hist[b] < x.hist[n.needle[n.anchor]] {
				n.anchor = i
			}
		}
		head := n.needle[:min(len(n.needle), bloomTail)]
		for i := 0; i+3 <= len(head); i++ {
			n.bits = append(n.bits, trigramBit(head[i], head[i+1], head[i+2]))
		}
		parts = append(parts, n)
	}
	return parts
}

// indexRare is bytes.Index anchored on the needle's rarest byte. bytes.Index
// anchors on the first byte, which for "terraform" means stopping at every
// t in the text and comparing; anchoring on the m instead runs IndexByte,
// which is a SIMD sweep, over nearly the whole text between candidates.
func indexRare(text []byte, n needle) int {
	if len(n.needle) == 0 {
		return 0
	}
	if len(n.needle) == 1 {
		return bytes.IndexByte(text, n.needle[0])
	}
	rare := n.needle[n.anchor]
	// The anchor byte cannot sit before offset anchor, nor so late that the
	// needle's tail overruns the text.
	for i := n.anchor; i+len(n.needle)-n.anchor <= len(text); {
		j := bytes.IndexByte(text[i:], rare)
		if j < 0 {
			return -1
		}
		start := i + j - n.anchor
		if start+len(n.needle) <= len(text) && bytes.Equal(text[start:start+len(n.needle)], n.needle) {
			return start
		}
		i += j + 1
	}
	return -1
}

// Stats is the index's size, for the settings view and the benchmarks.
type Stats struct {
	Sources int
	Turns   int
	// Bytes is the text held in memory, which is what the budgets bound;
	// Filters is the block filters beside it, a quarter of Bytes.
	Bytes   int64
	Filters int64
}

// Stats counts what the index holds.
func (x *Index) Stats() Stats {
	x.mu.RLock()
	defer x.mu.RUnlock()
	var st Stats
	for _, s := range x.sessions {
		st.Sources++
		st.Turns += s.turns
		st.Bytes += int64(len(s.text))
		st.Filters += int64(len(s.blooms)) * bloomBits / 8
	}
	return st
}
