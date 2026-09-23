package search

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/tracing"
)

// Target is one board session whose conversation the index should hold.
type Target struct {
	// Key is the board's session id, which is what a hit reports.
	Key string
	// Tool selects the transcript format: ToolClaude, ToolCodex, ToolOpenCode,
	// or any other name for the generic reader.
	Tool string
	// Path is the transcript file. Empty for opencode, which keeps its
	// conversations in a database addressed by AgentID.
	Path string
	// AgentID is the tool's own conversation id.
	AgentID string
}

// Progress is what one Refresh did, so the cost of the ticker is a number.
type Progress struct {
	// Done is false when the budget ran out with sources still behind; the
	// caller should run another pass soon rather than wait for the ticker.
	Done bool
	// Changed reports whether the index differs from before the pass, which
	// is when a query on screen is worth re-asking.
	Changed bool
	Files   int
	Bytes   int
	Rows    int
	Elapsed time.Duration
}

// refreshPhases is where one Refresh spent its pass, for the trace.
//
// Three phases, because they fail differently and the pass has no other way to
// say which one it was in: the scan is a stat per source and goes slow when the
// filesystem does, the read is bounded by the byte budget and goes slow when a
// session has been busy, and opencode is a database on the other side of a
// connection. A single number for the pass cannot tell those apart, which is
// the whole reason the board's history feels slow for reasons nobody can name.
type refreshPhases struct {
	on                      bool
	scanned, read, opencode time.Time
	targets, stale          int
	fileRows, opencodeRows  int
}

// mark timestamps the end of a phase, and nothing at all when the pass is not
// traced.
func (r *refreshPhases) mark(at *time.Time) {
	if !r.on {
		return
	}
	*at = time.Now()
}

// emit lays the phases under the pass. A phase whose end was never marked ran
// until the pass returned -- that is what an early return out of the read loop
// means -- so it is drawn to the end rather than dropped, which would hide the
// budget exhaustion that caused it.
func (r *refreshPhases) emit(start time.Time, p Progress) {
	finish := time.Now()
	trace := tracing.NewTrace("search.refresh", start, finish,
		tracing.Attr{Key: "targets", Value: r.targets},
		tracing.Attr{Key: "stale", Value: r.stale},
		tracing.Attr{Key: "files", Value: p.Files},
		tracing.Attr{Key: "bytes", Value: p.Bytes},
		tracing.Attr{Key: "rows", Value: p.Rows},
		tracing.Attr{Key: "done", Value: p.Done},
		tracing.Attr{Key: "changed", Value: p.Changed})
	at := start
	phase := func(name string, end time.Time, attrs ...tracing.Attr) {
		if end.IsZero() {
			end = finish
		}
		trace.Child(name, at, end, attrs...)
		at = end
	}
	phase("search.scan", r.scanned, tracing.Attr{Key: "stale", Value: r.stale})
	if !r.scanned.IsZero() {
		phase("search.read", r.read,
			tracing.Attr{Key: "files", Value: p.Files},
			tracing.Attr{Key: "bytes", Value: p.Bytes},
			tracing.Attr{Key: "rows", Value: r.fileRows})
	}
	if !r.opencode.IsZero() {
		phase("search.opencode", r.opencode, tracing.Attr{Key: "rows", Value: r.opencodeRows})
	}
	trace.Emit()
}

// Refresh brings the index toward the targets, reading at most budgetBytes
// of transcript before returning.
//
// Targets whose file grew are read newest-first so a live session is
// current before a backlog is. A file smaller than its resume offset was
// rotated or truncated and is rebuilt from zero; so is a key whose path
// changed, which is a row that now holds a different conversation. Only
// complete lines are consumed: a transcript is appended to mid-line while
// its session streams, and half a JSON row parses as nothing. Parsing runs
// outside the lock; the lock is held only to splice the result in.
func (x *Index) Refresh(targets []Target, budgetBytes int) (p Progress, err error) {
	start := time.Now()
	// Registered before the one below so it runs after it, with the elapsed
	// time the caller is handed already on the Progress it reports.
	var phases refreshPhases
	if tracing.Enabled() {
		phases.on = true
		defer func() { phases.emit(start, p) }()
	}
	defer func() { p.Elapsed = time.Since(start) }()

	wanted := make(map[string]bool, len(targets))
	var stale []pending
	x.mu.RLock()
	for _, t := range targets {
		if t.Key == "" {
			continue
		}
		wanted[t.Key] = true
		if t.Tool == ToolOpenCode {
			continue
		}
		st := stampOfFile(t.Path)
		if st.size == 0 && st.mtime == 0 {
			continue
		}
		prev, known := x.sessions[t.Key]
		if known && prev.path == t.Path {
			same, measured := st.unchanged(t.Path, prev.stamp())
			st = measured
			if same {
				continue
			}
		}
		size, mtime := st.size, st.mtime
		var snapshot session
		if known {
			snapshot = *prev
		}
		stale = append(stale, pending{target: t, size: size, mtime: mtime, tail: st.tail, tailRead: st.tailRead, known: known && prev.path == t.Path, prev: snapshot})
	}
	x.mu.RUnlock()
	sort.Slice(stale, func(i, j int) bool { return stale[i].mtime > stale[j].mtime })
	phases.mark(&phases.scanned)
	phases.targets, phases.stale = len(targets), len(stale)

	spent := 0
	// One unreadable file must not stall every other target behind it: the
	// pass goes on and reports the first failure once it has done the rest.
	var firstErr error
	for _, s := range stale {
		if spent >= budgetBytes {
			p.Done = false
			return p, firstErr
		}
		read, rows, err := x.indexFile(s, budgetBytes-spent)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", s.target.Path, err)
			}
			continue
		}
		spent += read
		if read > 0 || rows > 0 {
			p.Files++
			p.Changed = true
		}
		p.Bytes += read
		p.Rows += rows
	}
	phases.mark(&phases.read)
	phases.fileRows = p.Rows
	if spent >= budgetBytes {
		return p, firstErr
	}
	if firstErr != nil {
		// What was read is kept; the failure is reported now, and the caller
		// retries on its ticker rather than in a tight loop.
		return p, firstErr
	}

	changed, rows, err := x.refreshOpenCode(targets)
	phases.mark(&phases.opencode)
	phases.opencodeRows = rows
	if err != nil {
		return p, err
	}
	p.Rows += rows
	p.Changed = p.Changed || changed

	p.Changed = x.prune(wanted) || p.Changed
	p.Done = true
	return p, nil
}

type pending struct {
	target   Target
	size     int64
	mtime    int64
	tail     uint64
	tailRead bool
	known    bool
	prev     session
}

// indexFile catches one file up, reading at most budget bytes. read is what
// it consumed and rows how many turns it added.
func (x *Index) indexFile(s pending, budget int) (read, rows int, err error) {
	from := int64(0)
	if s.known && s.prev.indexed <= s.size {
		from = s.prev.indexed
	}
	rebuild := !s.known || s.prev.indexed > s.size
	if from == s.size && !rebuild {
		// Nothing new to read; remember the touch so the next pass is a stat.
		x.stamp(s.target.Key, stamp{size: s.size, mtime: s.mtime, tail: s.tail, tailRead: s.tailRead})
		return 0, 0, nil
	}

	file, err := os.Open(s.target.Path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	length := min(s.size-from, int64(budget))
	data := make([]byte, length)
	if _, err := io.ReadFull(io.NewSectionReader(file, from, length), data); err != nil {
		return len(data), 0, err
	}
	cut := bytes.LastIndexByte(data, '\n')
	if cut < 0 {
		// One incomplete line longer than the budget; the offset stays put
		// and the next pass, with the line hopefully finished, takes it whole.
		// The stamp is left behind too, so the file stays stale until then.
		x.splice(s.target, rebuild, from, stamp{size: -1, mtime: -1}, nil, nil, 0)
		return len(data), 0, nil
	}
	// Results are worth parsing only while a PR-creating call can be waiting
	// on one: from an earlier pass, or from this chunk. Otherwise every tool
	// result that happens to hold a pull request URL would be decoded for
	// nothing, and the corpus is full of them.
	chunk := data[:cut]
	calls := len(s.prev.opening) > 0 || bytes.Contains(chunk, prCreateMark) || bytes.Contains(chunk, ghPrMark) || bytes.Contains(chunk, pollMark) || bytes.Contains(chunk, waitMark)
	text, found, turns := x.parseChunk(s.target.Tool, chunk, calls)
	indexed := from + int64(cut) + 1
	size, mtime := s.size, s.mtime
	if indexed < s.size {
		// The budget stopped short of the end: leave the stamp behind so the
		// next pass sees the file as still stale.
		size, mtime = -1, -1
	}
	x.splice(s.target, rebuild, indexed, stamp{size: size, mtime: mtime, tail: s.tail, tailRead: s.tailRead}, text, found, turns)
	return len(data), turns, nil
}

// parallelParseBytes is the chunk size past which a chunk's lines are parsed
// by every CPU. Below it the goroutines cost more than they save.
const parallelParseBytes = 512 << 10

// parseChunk turns complete transcript lines into the index's text: one
// lowercased turn per line, in file order. A large chunk — a cold read of a
// long session — is split into contiguous line ranges parsed concurrently
// and joined in order, so a cold build is bounded by the JSON decoder times
// the core count rather than by one core.
func (x *Index) parseChunk(tool string, data []byte, withCalls bool) (text []byte, calls []toolCall, turns int) {
	workers := runtime.GOMAXPROCS(0)
	if len(data) < parallelParseBytes || workers < 2 {
		return x.parseLines(tool, data, withCalls)
	}
	ranges := splitAtLines(data, workers)
	texts := make([][]byte, len(ranges))
	kept := make([][]toolCall, len(ranges))
	counts := make([]int, len(ranges))
	var wg sync.WaitGroup
	for i, r := range ranges {
		wg.Add(1)
		go func(i int, r []byte) {
			defer wg.Done()
			texts[i], kept[i], counts[i] = x.parseLines(tool, r, withCalls)
		}(i, r)
	}
	wg.Wait()
	for i := range texts {
		text = append(text, texts[i]...)
		calls = append(calls, kept[i]...)
		turns += counts[i]
	}
	return text, calls, turns
}

// parseLines also returns the tool calls that matter to the work tracker:
// commands that open a pull request and results that print a URL. Those come
// out of the same pass because the transcript is read once, under a budget,
// and a second reader of the same files would double the cost of the ticker.
func (x *Index) parseLines(tool string, data []byte, withCalls bool) (text []byte, calls []toolCall, turns int) {
	var sb bytes.Buffer
	for remaining := data; len(remaining) > 0; {
		var line []byte
		line, remaining, _ = bytes.Cut(remaining, []byte{'\n'})
		turns += writeSegments(&sb, LineSegments(tool, line, x.limits))
		if withCalls {
			calls = append(calls, lineCalls(tool, line)...)
		}
	}
	return sb.Bytes(), calls, turns
}

// writeSegments appends segments to a session's buffer in the index's line
// format: every line is its kind byte, its lowercased text, a newline. A
// segment's own newlines start lines too, each re-stamped with the kind and
// the continuation bit, so the first byte of any line names what wrote it
// and only a segment's first line counts as a turn. It returns the turns
// written, which is what the session's turn count and recency measure in.
func writeSegments(sb *bytes.Buffer, segments []Segment) (turns int) {
	for _, seg := range segments {
		if seg.Text == "" {
			continue
		}
		for i, line := range strings.Split(strings.ToLower(seg.Text), "\n") {
			mark := byte(seg.Kind)
			if i > 0 {
				mark |= continuation
			}
			sb.WriteByte(mark)
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		turns++
	}
	return turns
}

// turnStarts counts the turns beginning in text[from:to]: lines whose
// kind byte lacks the continuation bit.
func turnStarts(text []byte, from, to int) int {
	n := 0
	for i := from; i < to; i++ {
		if (i == 0 || text[i-1] == '\n') && i < len(text) && text[i]&continuation == 0 && text[i] != '\n' {
			n++
		}
	}
	return n
}

// splitAtLines cuts data into up to n ranges on line boundaries.
func splitAtLines(data []byte, n int) [][]byte {
	var ranges [][]byte
	step := len(data) / n
	for len(data) > 0 {
		if len(ranges) == n-1 || step >= len(data) {
			ranges = append(ranges, data)
			break
		}
		cut := bytes.IndexByte(data[step:], '\n')
		if cut < 0 {
			ranges = append(ranges, data)
			break
		}
		ranges = append(ranges, data[:step+cut+1])
		data = data[step+cut+1:]
	}
	return ranges
}

// splice records a file's state and appends (or, on rebuild, replaces) its
// text under the write lock, then enforces the session and total budgets.
func (x *Index) splice(t Target, rebuild bool, indexed int64, st stamp, text []byte, calls []toolCall, turns int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	s := x.sessions[t.Key]
	if s == nil || rebuild || s.path != t.Path {
		if s != nil {
			x.total -= len(s.text)
			x.count(s.text, -1)
		}
		s = &session{tool: t.Tool, path: t.Path}
		x.sessions[t.Key] = s
	}
	s.indexed, s.size, s.mtime, s.tail, s.tailRead = indexed, st.size, st.mtime, st.tail, st.tailRead
	was := len(s.text)
	s.text = append(s.text, text...)
	s.recordCreations(calls)
	s.turns += turns
	x.total += len(text)
	x.count(text, 1)
	if len(text) > 0 {
		s.addTrigrams(was)
	}
	x.last.query, x.last.hits = "", nil
	if len(s.text) > x.sessionBytes {
		x.trim(s, x.sessionBytes)
	}
	for x.total > x.totalBytes {
		largest := s
		for _, other := range x.sessions {
			if len(other.text) > len(largest.text) {
				largest = other
			}
		}
		if len(largest.text) == 0 {
			break
		}
		x.trim(largest, len(largest.text)/2)
	}
}

// trim keeps a session's newest keep bytes, cut on a turn boundary, and
// releases the rest.
func (x *Index) trim(s *session, keep int) {
	drop := len(s.text) - keep
	if nl := bytes.IndexByte(s.text[drop:], '\n'); nl >= 0 {
		drop += nl + 1
	}
	s.turns -= turnStarts(s.text, 0, drop)
	x.count(s.text[:drop], -1)
	s.text = append([]byte(nil), s.text[drop:]...)
	x.total -= drop
	s.rebuildBlooms()
}

// count adds or removes text from the byte histogram the query anchor reads.
func (x *Index) count(text []byte, sign int64) {
	for _, b := range text {
		x.hist[b] += sign
	}
}

func (x *Index) stamp(key string, st stamp) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if s := x.sessions[key]; s != nil {
		s.size, s.mtime, s.tail, s.tailRead = st.size, st.mtime, st.tail, st.tailRead
	}
}

// prune drops sources the board no longer lists.
func (x *Index) prune(wanted map[string]bool) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	pruned := false
	for key, s := range x.sessions {
		if !wanted[key] {
			x.total -= len(s.text)
			x.count(s.text, -1)
			delete(x.sessions, key)
			pruned = true
		}
	}
	if pruned {
		x.last.query, x.last.hits = "", nil
	}
	return pruned
}

// recordCreations pairs each PR-creating command with the result that came
// back for it and keeps the pull request URLs that result printed.
//
// The pairing is by tool-call id rather than by adjacency because the result
// can land in a later chunk than the call, and because a session runs other
// commands whose output happens to contain a pull request URL -- a PR list, a
// changelog -- which are not things it opened.
func (s *session) recordCreations(calls []toolCall) {
	for _, call := range calls {
		if !call.result {
			if createsPR.MatchString(call.text) || call.poll {
				if s.opening == nil {
					s.opening = map[string]bool{}
				}
				s.opening[call.id] = createsPR.MatchString(call.text) && !call.poll
			}
			continue
		}
		creating, pending := s.opening[call.id]
		if !pending {
			continue
		}
		delete(s.opening, call.id)
		urls := pullURL.FindAllString(call.text, -1)
		if !creating {
			// A poll may finish any command; only the wrapper's confirmation
			// establishes creation when the original call returned no URL.
			urls = nil
			for _, match := range wrapperCreated.FindAllStringSubmatch(call.text, -1) {
				if match[1] == match[3] {
					urls = append(urls, match[2])
				}
			}
		}
		for _, url := range urls {
			if bytes.Contains(s.created, []byte(url+"\n")) {
				continue
			}
			s.created = append(s.created, url...)
			s.created = append(s.created, '\n')
		}
	}
}
