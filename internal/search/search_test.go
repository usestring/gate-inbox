package search

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func openTest(t *testing.T, opts Options) *Index {
	t.Helper()
	x := New(opts)
	t.Cleanup(func() { x.Close() })
	return x
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	// mtime resolution on some filesystems is coarse; a later write must
	// look later to the stat that gates the refresh.
	future := time.Now().Add(time.Duration(len(lines)) * time.Second)
	_ = os.Chtimes(path, future, future)
}

func userLine(text string) string {
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	return string(line)
}

func assistantLine(text, command string) string {
	line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "text", "text": text},
		map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": command}},
	}}})
	return string(line)
}

func refreshAll(t *testing.T, x *Index, targets []Target) Progress {
	t.Helper()
	var total Progress
	for i := 0; i < 100; i++ {
		p, err := x.Refresh(targets, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		total.Changed = total.Changed || p.Changed
		total.Rows += p.Rows
		total.Bytes += p.Bytes
		if p.Done {
			total.Done = true
			return total
		}
	}
	t.Fatal("refresh never completed")
	return total
}

func hits(t *testing.T, x *Index, query string) []string {
	t.Helper()
	found, ok, err := x.Search(query, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return nil
	}
	var keys []string
	for _, h := range found {
		keys = append(keys, h.Key)
	}
	sort.Strings(keys)
	return keys
}

// ranked is the hit order the index answers with, best first.
func ranked(t *testing.T, x *Index, query string) []Hit {
	t.Helper()
	found, _, err := x.Search(query, 100)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// The session that mentioned the query most recently outranks one that
// mentioned it long ago, and among equally recent mentions the one that
// mentioned it more outranks the one that mentioned it once. Counts are
// exact, and a wildcard query is counted by its first segment.
func TestHitsRankByRecencyThenCount(t *testing.T) {
	x := openTest(t, Options{})
	dir := t.TempDir()
	write := func(name string, lines ...string) Target {
		path := filepath.Join(dir, name+".jsonl")
		writeLines(t, path, lines...)
		return Target{Key: name, Tool: ToolClaude, Path: path}
	}
	filler := make([]string, 60)
	for i := range filler {
		filler[i] = userLine(fmt.Sprintf("unrelated turn %02d", i))
	}
	targets := []Target{
		write("stale", append([]string{userLine("we discussed the proxy zone limit"), userLine("the proxy zone limit again")}, filler...)...),
		write("fresh", append(append([]string{}, filler...), userLine("hitting the proxy zone limit now"))...),
		write("chatty", append(append([]string{}, filler...), userLine("proxy zone limit"), userLine("proxy zone limit"), userLine("proxy zone limit"))...),
		write("quiet", filler...),
	}
	refreshAll(t, x, targets)

	got := ranked(t, x, "proxy zone limit")
	var order []string
	for _, h := range got {
		order = append(order, fmt.Sprintf("%s:%d", h.Key, h.Count))
	}
	if fmt.Sprint(order) != "[chatty:3 fresh:1 stale:2]" {
		t.Fatalf("rank = %v", order)
	}
	if got[0].Score <= got[1].Score || got[1].Score <= got[2].Score {
		t.Fatalf("scores not descending: %+v", got)
	}
	// A wildcard query ranks the same way and counts each occurrence once.
	got = ranked(t, x, "proxy*limit")
	if len(got) != 3 || got[0].Key != "chatty" || got[0].Count != 3 || got[1].Key != "fresh" {
		t.Fatalf("wildcard rank = %+v", got)
	}
	// A block reports at most maxHitsPerBlock occurrences, newest-first in
	// weight since every one is scored by its own recency.
	if got := ranked(t, x, "unrelated turn"); len(got) != 4 || got[0].Count != maxHitsPerBlock {
		t.Fatalf("count is capped per block: %+v", got)
	}
}

// What the operator typed outranks the same term the agent said, which
// outranks the term in a command, which outranks it in a tool's output; an
// injected system reminder counts least of all. All at equal recency.
func TestHitsWeighWhoSaidIt(t *testing.T) {
	x := openTest(t, Options{})
	dir := t.TempDir()
	write := func(name string, lines ...string) Target {
		path := filepath.Join(dir, name+".jsonl")
		writeLines(t, path, lines...)
		return Target{Key: name, Tool: ToolClaude, Path: path}
	}
	toolUse := func(command string) string {
		line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": command}},
		}}})
		return string(line)
	}
	toolResult := func(text string) string {
		line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t", "content": text},
		}}})
		return string(line)
	}
	assistant := func(text string) string {
		line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": text},
		}}})
		return string(line)
	}
	targets := []Target{
		write("typed", userLine("let's look at the mitm proxy")),
		write("said", assistant("the mitm proxy is on port 8080")),
		write("ran", toolUse("go test -run 'mitm proxy' ./commons")),
		write("printed", toolResult("mitm proxy: 3 tests passed")),
		write("injected", userLine("<system-reminder>the mitm proxy hook fired</system-reminder>")),
	}
	refreshAll(t, x, targets)
	got := ranked(t, x, "mitm proxy")
	var order []string
	for _, h := range got {
		order = append(order, h.Key)
	}
	if fmt.Sprint(order) != "[typed said ran printed injected]" {
		t.Fatalf("rank = %v", order)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Score >= got[i-1].Score {
			t.Fatalf("scores not strictly descending: %+v", got)
		}
	}
	// Two mentions in tool output do not beat one the operator typed.
	x2 := openTest(t, Options{})
	targets = []Target{
		write("typed2", userLine("the mitm proxy again")),
		write("printed2", toolResult("mitm proxy"), toolResult("mitm proxy"), toolResult("mitm proxy")),
	}
	refreshAll(t, x2, targets)
	if got := ranked(t, x2, "mitm proxy"); len(got) != 2 || got[0].Key != "typed2" {
		t.Fatalf("three tool-output mentions outranked one typed mention: %+v", got)
	}
}

// turnsBefore agrees with a plain count at every offset, across the running
// count's segment boundaries and after a trim rebuilt it.
func TestTurnsBeforeMatchesACount(t *testing.T) {
	x := openTest(t, Options{SessionBytes: 3 * newlineStep})
	a := filepath.Join(t.TempDir(), "a.jsonl")
	var lines []string
	for i := 0; i < 400; i++ {
		lines = append(lines, userLine(fmt.Sprintf("turn %03d %s", i, strings.Repeat("z", i%50))))
	}
	writeLines(t, a, lines...)
	refreshAll(t, x, []Target{{Key: "s", Tool: ToolClaude, Path: a}})
	s := x.sessions["s"]
	if len(s.text) > 3*newlineStep || len(s.newlines) < 2 {
		t.Fatalf("fixture: %d bytes, %d segments", len(s.text), len(s.newlines))
	}
	for pos := 0; pos <= len(s.text); pos += 97 {
		if got, want := s.turnsBefore(pos), turnStarts(s.text, 0, pos); got != want {
			t.Fatalf("turnsBefore(%d) = %d, count = %d", pos, got, want)
		}
	}
	// A multi-line turn is one turn: its continuation lines do not count.
	x2 := openTest(t, Options{})
	b := filepath.Join(t.TempDir(), "b.jsonl")
	writeLines(t, b, userLine("first"), userLine("a turn\nof three\nlines"), userLine("last"))
	refreshAll(t, x2, []Target{{Key: "b", Tool: ToolClaude, Path: b}})
	s2 := x2.sessions["b"]
	if s2.turns != 3 || turnStarts(s2.text, 0, len(s2.text)) != 3 {
		t.Fatalf("multi-line turn counted as lines: turns=%d starts=%d", s2.turns, turnStarts(s2.text, 0, len(s2.text)))
	}
	if kind := s2.kindAt(bytes.Index(s2.text, []byte("of three"))); kind != KindUser {
		t.Fatalf("continuation line kind = %v", kind)
	}
	if s.turnsBefore(len(s.text)) != s.turns {
		t.Fatalf("turns before the end = %d, turns = %d", s.turnsBefore(len(s.text)), s.turns)
	}
}

func TestSearchFindsProseCommandsAndResults(t *testing.T) {
	x := openTest(t, Options{})
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	writeLines(t, a, userLine("please fix the mitm flake"), assistantLine("On it.", "go test ./commons/mitm -run TestProxy"))
	writeLines(t, b, userLine("rotate the r2 token"), `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"host db-stage-3.example.test reachable"}]}}`)
	targets := []Target{{Key: "s1", Tool: ToolClaude, Path: a}, {Key: "s2", Tool: ToolClaude, Path: b}}

	p := refreshAll(t, x, targets)
	if !p.Changed || p.Rows != 5 { // a turn per prose block, tool call and tool result
		t.Fatalf("cold pass: %+v", p)
	}
	for query, want := range map[string][]string{
		"mitm flake":              {"s1"},
		"MITM":                    {"s1"},
		"-run TestProxy":          {"s1"},
		"db-stage-3.example.test": {"s2"},
		"token":                   {"s2"},
		"nothing here":            nil,
	} {
		if got := hits(t, x, query); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%q: got %v want %v", query, got, want)
		}
	}
	if keys, ok, _ := x.Search("ab", 10); ok || keys != nil {
		t.Fatalf("a two-rune query must be refused, got ok=%v %v", ok, keys)
	}
	if keys, ok, _ := x.Search("a*b", 10); ok || keys != nil {
		t.Fatalf("the wildcard does not count toward the minimum, got ok=%v %v", ok, keys)
	}
}

func TestSearchWildcardAndLiteralMetacharacters(t *testing.T) {
	x := openTest(t, Options{})
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	writeLines(t, a, userLine("host db-stage-3.example.test is a snake_case_name with a 50% chance [x] ok?"))
	writeLines(t, b, userLine("host dbXstageX3.example.test and snakeYcaseYname"))
	targets := []Target{{Key: "s1", Tool: ToolClaude, Path: a}, {Key: "s2", Tool: ToolClaude, Path: b}}
	refreshAll(t, x, targets)
	for query, want := range map[string][]string{
		"db-*-3.example.test":  {"s1"},
		"host db*example.test": {"s1", "s2"},
		"snake_case":           {"s1"}, // _ is literal, not LIKE's any-character
		"50% chance":           {"s1"}, // % is literal, not LIKE's any-run
		"[x] ok?":              {"s1"}, // GLOB metacharacters are literal too
		"snake*name":           {"s1", "s2"},
		"DB-STAGE-*":           {"s1"},
	} {
		if got := hits(t, x, query); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%q: got %v want %v", query, got, want)
		}
	}
}

// A block's filter admits every term that starts inside the block, including
// one that runs past its end, and rejects a term the block lacks.
func TestBloomAdmitsStraddlingTermsAndRejectsAbsentOnes(t *testing.T) {
	x := openTest(t, Options{Limits: Limits{Text: 1 << 20, Input: 1 << 10, Result: 1 << 10}})
	a := filepath.Join(t.TempDir(), "a.jsonl")
	// Two blocks: filler, then a marker placed so it straddles the boundary.
	filler := strings.Repeat("f", blockBytes-10)
	writeLines(t, a, userLine(filler+"straddle-marker-xyz"), userLine(strings.Repeat("g", 200)+" qwertyuiop"))
	targets := []Target{{Key: "s1", Tool: ToolClaude, Path: a}}
	refreshAll(t, x, targets)
	s := x.sessions["s1"]
	if len(s.blooms) != 2 {
		t.Fatalf("expected 2 blocks, got %d over %d bytes", len(s.blooms), len(s.text))
	}
	for query, want := range map[string]string{
		"straddle-marker-xyz": "[s1]",
		"marker-xyz":          "[s1]",
		"qwertyuiop":          "[s1]",
		"fffstraddle":         "[s1]",
		"nowhere-at-all":      "[]",
		"strad*yuiop":         "[s1]", // wildcard: segments in different blocks
		"qwerty*straddle":     "[]",   // wildcard in the wrong order
	} {
		if got := hits(t, x, query); fmt.Sprint(got) != want {
			t.Errorf("%q: got %v want %s", query, got, want)
		}
	}
	first := x.splitQuery("qwertyuiop")
	if s.blooms[0].mayHold(first[0]) || !s.blooms[1].mayHold(first[0]) {
		t.Fatalf("filter placed the second block's term wrongly: %v %v", s.blooms[0].mayHold(first[0]), s.blooms[1].mayHold(first[0]))
	}
}

// indexRare must agree with bytes.Index for every anchor and every needle,
// including needles that straddle the text's ends and repeated bytes.
func TestIndexRareAgreesWithBytesIndex(t *testing.T) {
	text := []byte("the terraform apply in tenant t7 tested the tt tunnel; terraform again at the end terrafor")
	for _, sep := range []string{"terraform", "tt", "t7", "the", "end terrafor", "terrafors", "x", "a", "form apply", "terraform again at the end terrafor", " "} {
		for anchor := range sep {
			n := needle{needle: []byte(sep), anchor: anchor}
			for from := 0; from <= len(text); from += 7 {
				got, want := indexRare(text[from:], n), bytes.Index(text[from:], []byte(sep))
				if got != want {
					t.Fatalf("indexRare(%q[%d:], %q@%d) = %d, bytes.Index = %d", text, from, sep, anchor, got, want)
				}
			}
		}
	}
}

// The histogram follows appends, trims and prunes, so the anchor is chosen
// against what the index actually holds.
func TestHistogramTracksTheIndex(t *testing.T) {
	x := openTest(t, Options{SessionBytes: 300})
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	writeLines(t, a, userLine("zzz"), userLine(strings.Repeat("q", 400)))
	writeLines(t, b, userLine("zz"))
	targets := []Target{{Key: "a", Tool: ToolClaude, Path: a}, {Key: "b", Tool: ToolClaude, Path: b}}
	refreshAll(t, x, targets)
	sum := func() (n int64) {
		for _, c := range x.hist {
			n += c
		}
		return n
	}
	if got := sum(); got != int64(x.Stats().Bytes) {
		t.Fatalf("histogram counts %d bytes, index holds %d", got, x.Stats().Bytes)
	}
	if x.hist['z'] != 2 { // a's zzz was trimmed by the session cap; b's zz remains
		t.Fatalf("z count after trim: %d", x.hist['z'])
	}
	refreshAll(t, x, targets[:1])
	if x.hist['z'] != 0 || sum() != int64(x.Stats().Bytes) {
		t.Fatalf("after prune: z=%d sum=%d bytes=%d", x.hist['z'], sum(), x.Stats().Bytes)
	}
}

func TestMatchHonoursTheWildcardInOrder(t *testing.T) {
	for _, c := range []struct {
		text, query string
		want        bool
	}{
		{"fatal: could not resolve db-primary-07", "db-primary-07", true},
		{"fatal: could not resolve db-primary-07", "db-*-07", true},
		{"fatal: could not resolve db-primary-07", "resolve*07", true},
		{"fatal: could not resolve db-primary-07", "07*db", false},
		{"fatal: could not resolve db-primary-07", "*primary*", true},
		{"fatal: could not resolve db-primary-07", "db-**07", true},
		{"abc", "abcd*", false},
		{"anything", "*", true},
		{"", "", true},
	} {
		if got := Match(c.text, c.query); got != c.want {
			t.Errorf("Match(%q, %q) = %v want %v", c.text, c.query, got, c.want)
		}
	}
}

func TestIdlePassIsFreeAndAppendsAreIncremental(t *testing.T) {
	x := openTest(t, Options{})
	a := filepath.Join(t.TempDir(), "a.jsonl")
	writeLines(t, a, userLine("first thing"))
	targets := []Target{{Key: "s1", Tool: ToolClaude, Path: a}}
	refreshAll(t, x, targets)

	p, err := x.Refresh(targets, 1<<20)
	if err != nil || p.Changed || p.Bytes != 0 || !p.Done {
		t.Fatalf("idle pass: %+v %v", p, err)
	}

	writeLines(t, a, userLine("second thing"))
	p = refreshAll(t, x, targets)
	if p.Rows != 1 || p.Bytes >= int(fileSize(t, a)) {
		t.Fatalf("append pass read %d bytes for %d rows; want just the new line", p.Bytes, p.Rows)
	}
	if got := hits(t, x, "second thing"); fmt.Sprint(got) != "[s1]" {
		t.Fatalf("appended line not found: %v", got)
	}
	if got := hits(t, x, "first thing"); fmt.Sprint(got) != "[s1]" {
		t.Fatalf("earlier line lost: %v", got)
	}
}

func TestHalfWrittenLineWaits(t *testing.T) {
	x := openTest(t, Options{})
	a := filepath.Join(t.TempDir(), "a.jsonl")
	writeLines(t, a, userLine("complete line"))
	if err := os.WriteFile(a, []byte(userLine("complete line")+"\n"+`{"type":"user","message":{"role":"user","content":"half wri`), 0o644); err != nil {
		t.Fatal(err)
	}
	targets := []Target{{Key: "s1", Tool: ToolClaude, Path: a}}
	refreshAll(t, x, targets)
	if got := hits(t, x, "half wri"); got != nil {
		t.Fatalf("half a line was indexed: %v", got)
	}
	writeLines(t, a, `tten line"}}`)
	refreshAll(t, x, targets)
	if got := hits(t, x, "half written line"); fmt.Sprint(got) != "[s1]" {
		t.Fatalf("finished line not found: %v", got)
	}
}

func TestBudgetSpreadsAColdIndexOverPasses(t *testing.T) {
	x := openTest(t, Options{})
	a := filepath.Join(t.TempDir(), "a.jsonl")
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, userLine(fmt.Sprintf("message number %03d %s", i, strings.Repeat("pad ", 50))))
	}
	writeLines(t, a, lines...)
	targets := []Target{{Key: "s1", Tool: ToolClaude, Path: a}}
	passes := 0
	for {
		p, err := x.Refresh(targets, 4<<10)
		if err != nil {
			t.Fatal(err)
		}
		passes++
		if p.Done {
			break
		}
		if passes > 1000 {
			t.Fatal("budgeted refresh never finished")
		}
	}
	if passes < 5 {
		t.Fatalf("a 4KB budget over %d bytes finished in %d passes", fileSize(t, a), passes)
	}
	for _, want := range []string{"message number 000", "message number 199"} {
		if got := hits(t, x, want); fmt.Sprint(got) != "[s1]" {
			t.Errorf("%q: %v", want, got)
		}
	}
}

func TestTruncationAndReassignmentRebuild(t *testing.T) {
	x := openTest(t, Options{})
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	writeLines(t, a, userLine("old conversation content"), userLine("more old content"))
	targets := []Target{{Key: "s1", Tool: ToolClaude, Path: a}}
	refreshAll(t, x, targets)

	// Truncated: the file shrank below its resume offset.
	if err := os.WriteFile(a, []byte(userLine("fresh start")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refreshAll(t, x, targets)
	if got := hits(t, x, "old conversation"); got != nil {
		t.Fatalf("truncated content survived: %v", got)
	}
	if got := hits(t, x, "fresh start"); fmt.Sprint(got) != "[s1]" {
		t.Fatalf("rebuilt content missing: %v", got)
	}

	// Reassigned: the same board row now holds a different conversation.
	b := filepath.Join(dir, "b.jsonl")
	writeLines(t, b, userLine("resumed elsewhere"))
	refreshAll(t, x, []Target{{Key: "s1", Tool: ToolClaude, Path: b}})
	if got := hits(t, x, "fresh start"); got != nil {
		t.Fatalf("previous conversation survived reassignment: %v", got)
	}
	if got := hits(t, x, "resumed elsewhere"); fmt.Sprint(got) != "[s1]" {
		t.Fatalf("new conversation missing: %v", got)
	}
}

func TestDepartedRowsArePruned(t *testing.T) {
	x := openTest(t, Options{})
	dir := t.TempDir()
	var targets []Target
	for i := 0; i < 11; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%d.jsonl", i))
		writeLines(t, p, userLine(fmt.Sprintf("session %d says hello", i)))
		targets = append(targets, Target{Key: fmt.Sprintf("s%d", i), Tool: ToolClaude, Path: p})
	}
	refreshAll(t, x, targets)
	if got := hits(t, x, "says hello"); len(got) != len(targets) {
		t.Fatalf("indexed %d of %d", len(got), len(targets))
	}
	refreshAll(t, x, targets[:1])
	if got := hits(t, x, "says hello"); fmt.Sprint(got) != "[s0]" {
		t.Fatalf("pruning left %v", got)
	}
	if st := x.Stats(); st.Sources != 1 || st.Turns != 1 {
		t.Fatalf("stats after prune: %+v", st)
	}
}

// A session past its byte cap keeps its newest turns and forgets its oldest.
func TestSessionCapKeepsTheNewestTurns(t *testing.T) {
	x := openTest(t, Options{SessionBytes: 400})
	a := filepath.Join(t.TempDir(), "a.jsonl")
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, userLine(fmt.Sprintf("turn number %02d %s", i, strings.Repeat("x", 40))))
	}
	writeLines(t, a, lines...)
	targets := []Target{{Key: "s1", Tool: ToolClaude, Path: a}}
	refreshAll(t, x, targets)
	st := x.Stats()
	if st.Bytes > 400 || st.Turns >= 20 || st.Turns == 0 {
		t.Fatalf("cap not applied: %+v", st)
	}
	if got := hits(t, x, "turn number 19"); fmt.Sprint(got) != "[s1]" {
		t.Fatalf("newest turn lost: %v", got)
	}
	if got := hits(t, x, "turn number 00"); got != nil {
		t.Fatalf("oldest turn kept past the cap: %v", got)
	}
	// Appends keep landing after the cap took effect.
	writeLines(t, a, userLine("the very latest turn"))
	refreshAll(t, x, targets)
	if got := hits(t, x, "very latest"); fmt.Sprint(got) != "[s1]" {
		t.Fatalf("append after cap: %v", got)
	}
}

// One target that cannot be read must not stall the ones behind it, and the
// failure still reaches the caller.
func TestUnreadableTargetDoesNotStallTheOthers(t *testing.T) {
	x := openTest(t, Options{})
	dir := t.TempDir()
	good := filepath.Join(dir, "good.jsonl")
	writeLines(t, good, userLine("readable line"))
	// A directory stats fine and fails on read, which is the shape of a
	// transcript replaced by something else under the same name.
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(bad, later, later) // newest first: the failure comes before the good file
	targets := []Target{{Key: "bad", Tool: ToolClaude, Path: bad}, {Key: "good", Tool: ToolClaude, Path: good}}
	p, err := x.Refresh(targets, 1<<20)
	if err == nil {
		t.Fatal("an unreadable target was not reported")
	}
	if p.Rows != 1 || p.Done {
		t.Fatalf("the readable target behind the failure was not indexed: %+v", p)
	}
	if got := hits(t, x, "readable line"); fmt.Sprint(got) != "[good]" {
		t.Fatalf("after a failing pass: %v", got)
	}
}

// The total budget takes the oldest turns of the largest session first, and
// the sessions under it keep everything.
func TestTotalBudgetTrimsTheLargestSession(t *testing.T) {
	x := openTest(t, Options{TotalBytes: 2000})
	dir := t.TempDir()
	big := filepath.Join(dir, "big.jsonl")
	small := filepath.Join(dir, "small.jsonl")
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, userLine(fmt.Sprintf("big turn %02d %s", i, strings.Repeat("y", 60))))
	}
	writeLines(t, big, lines...)
	writeLines(t, small, userLine("small session says hello"))
	targets := []Target{{Key: "big", Tool: ToolClaude, Path: big}, {Key: "small", Tool: ToolClaude, Path: small}}
	refreshAll(t, x, targets)
	st := x.Stats()
	if st.Bytes > 2000 || st.Sources != 2 {
		t.Fatalf("budget not applied: %+v", st)
	}
	if got := hits(t, x, "says hello"); fmt.Sprint(got) != "[small]" {
		t.Fatalf("the small session was trimmed instead of the large one: %v", got)
	}
	if got := hits(t, x, "big turn 39"); fmt.Sprint(got) != "[big]" {
		t.Fatalf("the large session lost its newest turn: %v", got)
	}
	if got := hits(t, x, "big turn 00"); got != nil {
		t.Fatalf("the large session kept its oldest turn past the budget: %v", got)
	}
}

// A query that extends the previous one scans only the previous hits, and
// the shortcut never changes an answer: after new text arrives or the query
// changes shape, a full scan runs again.
func TestNarrowingNeverChangesTheAnswer(t *testing.T) {
	x := openTest(t, Options{})
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	writeLines(t, a, userLine("the collector rollout is stuck"))
	writeLines(t, b, userLine("the collector is fine"))
	targets := []Target{{Key: "a", Tool: ToolClaude, Path: a}, {Key: "b", Tool: ToolClaude, Path: b}}
	refreshAll(t, x, targets)
	if got := hits(t, x, "collector"); fmt.Sprint(got) != "[a b]" {
		t.Fatalf("full scan: %v", got)
	}
	if got := hits(t, x, "collector rollout"); fmt.Sprint(got) != "[a]" {
		t.Fatalf("narrowed scan: %v", got)
	}
	// Backspacing to a shorter query is not a narrowing and must widen again.
	if got := hits(t, x, "collector"); fmt.Sprint(got) != "[a b]" {
		t.Fatalf("widened scan: %v", got)
	}
	// New text in a session outside the last hits must be found.
	if got := hits(t, x, "collector rollout"); fmt.Sprint(got) != "[a]" {
		t.Fatalf("narrowed again: %v", got)
	}
	writeLines(t, b, userLine("now the collector rollout is stuck here too"))
	refreshAll(t, x, targets)
	if got := hits(t, x, "collector rollout is stuck"); fmt.Sprint(got) != "[a b]" {
		t.Fatalf("a refresh must drop the narrowing cache: %v", got)
	}
	// A wildcard in the previous query disables narrowing.
	if got := hits(t, x, "coll*stuck"); fmt.Sprint(got) != "[a b]" {
		t.Fatalf("wildcard: %v", got)
	}
	if got := hits(t, x, "coll*stuck here"); fmt.Sprint(got) != "[b]" {
		t.Fatalf("after a wildcard query: %v", got)
	}
}

// A large chunk is parsed by every core and joined in file order, so the
// result must be byte-identical to the serial parse.
func TestParallelParseMatchesSerial(t *testing.T) {
	x := openTest(t, Options{})
	var sb strings.Builder
	for i := 0; sb.Len() < 2*parallelParseBytes; i++ {
		sb.WriteString(userLine(fmt.Sprintf("line %05d %s", i, strings.Repeat("payload ", 20))))
		sb.WriteByte('\n')
		sb.WriteString(`{"type":"progress","data":{}}` + "\n")
	}
	data := []byte(strings.TrimSuffix(sb.String(), "\n"))
	parallel, pc, np := x.parseChunk(ToolClaude, data, true)
	serial, sc, ns := x.parseLines(ToolClaude, data, true)
	if np != ns || !bytes.Equal(parallel, serial) || len(pc) != len(sc) {
		t.Fatalf("parallel parse differs: %d vs %d turns, %d vs %d bytes", np, ns, len(parallel), len(serial))
	}
	if got := hits(t, x, "line 00000"); got != nil {
		t.Fatalf("parseChunk must not touch the index: %v", got)
	}
}

func TestCodexRolloutIsIndexed(t *testing.T) {
	x := openTest(t, Options{})
	r := filepath.Join(t.TempDir(), "rollout-2026-09-04T10-00-00-abc.jsonl")
	writeLines(t, r,
		`{"type":"session_meta","payload":{"id":"abc","cwd":"/w"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"port the brain to codex"}]}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"tools.exec_command({\"cmd\":\"go build ./...\"})"}}`,
		`{"type":"event_msg","payload":{"type":"agent_message","message":"unindexed duplicate"}}`,
	)
	refreshAll(t, x, []Target{{Key: "c1", Tool: ToolCodex, Path: r, AgentID: "abc"}})
	for query, want := range map[string]string{"brain to codex": "[c1]", "go build ./...": "[c1]", "unindexed": "[]"} {
		if got := hits(t, x, query); fmt.Sprint(got) != want {
			t.Errorf("%q: got %v want %s", query, got, want)
		}
	}
}

func TestOpenCodePartsAreIndexedIncrementally(t *testing.T) {
	ocPath := filepath.Join(tmuxtest.ScratchDir(t), "opencode.db")
	oc, err := sql.Open("sqlite", ocPath)
	if err != nil {
		t.Fatal(err)
	}
	defer oc.Close()
	if _, err := oc.Exec(`CREATE TABLE session_v2 (id TEXT PRIMARY KEY);
		CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, session, kind, data string, at int64) {
		if _, err := oc.Exec("INSERT INTO session_message VALUES (?, ?, ?, 0, ?, ?, ?)", id, session, kind, at, at, data); err != nil {
			t.Fatal(err)
		}
	}
	insert("p1", "oc-1", "user", `{"type":"user","text":"deploy the portal"}`, 100)
	insert("p2", "oc-1", "assistant", `{"type":"assistant","content":[{"type":"tool","name":"bash","state":{"input":{"command":"kubectl rollout restart"},"content":[{"type":"text","text":"restarted"}]}}]}`, 200)
	insert("p3", "oc-2", "user", `{"type":"user","text":"someone else's session"}`, 300)

	x := openTest(t, Options{OpenCodeDB: ocPath})
	targets := []Target{{Key: "s1", Tool: ToolOpenCode, AgentID: "oc-1"}}
	p := refreshAll(t, x, targets)
	if p.Rows != 3 {
		t.Fatalf("opencode cold pass: %+v (a tool part is an input turn and a result turn)", p)
	}
	for query, want := range map[string]string{"deploy the portal": "[s1]", "rollout restart": "[s1]", "restarted": "[s1]", "someone else": "[]"} {
		if got := hits(t, x, query); fmt.Sprint(got) != want {
			t.Errorf("%q: got %v want %s", query, got, want)
		}
	}
	// Nothing written: the stamp gate skips the query entirely.
	p, err = x.Refresh(targets, 1<<20)
	if err != nil || p.Changed {
		t.Fatalf("idle opencode pass: %+v %v", p, err)
	}
	insert("p4", "oc-1", "user", `{"type":"user","text":"later message"}`, 400)
	p = refreshAll(t, x, targets)
	if p.Rows != 1 {
		t.Fatalf("incremental opencode pass: %+v", p)
	}
	if got := hits(t, x, "later message"); fmt.Sprint(got) != "[s1]" {
		t.Fatalf("appended part: %v", got)
	}
}

func TestOpenCodeV2MessagesAreIndexed(t *testing.T) {
	ocPath := filepath.Join(tmuxtest.ScratchDir(t), "opencode.db")
	oc, err := sql.Open("sqlite", ocPath)
	if err != nil {
		t.Fatal(err)
	}
	defer oc.Close()
	if _, err := oc.Exec(`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, directory TEXT, title TEXT);
		CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT);`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, session, kind, data string, at int64) {
		if _, err := oc.Exec("INSERT INTO session_message VALUES (?, ?, ?, 0, ?, ?, ?)", id, session, kind, at, at, data); err != nil {
			t.Fatal(err)
		}
	}
	insert("m1", "oc-1", "user", `{"id":"msg_1","time":{"created":100},"text":"deploy the portal"}`, 100)
	insert("m2", "oc-1", "assistant", `{"id":"msg_2","time":{"created":200},"agent":"build","model":{},"content":[{"type":"text","text":"restarting"},{"type":"tool","id":"call_1","name":"shell","state":{"status":"completed","input":{"command":"gh pr create --title x"},"content":[{"type":"text","text":"created #42"}]}}]}`, 200)
	insert("m3", "oc-2", "user", `{"id":"msg_3","time":{"created":300},"text":"someone else's session"}`, 300)

	insert("m4", "oc-1", "idle", `{"text":"hidden idle event"}`, 400)

	x := openTest(t, Options{OpenCodeDB: ocPath})
	targets := []Target{{Key: "s1", Tool: ToolOpenCode, AgentID: "oc-1"}}
	refreshAll(t, x, targets)
	for query, want := range map[string]string{"deploy the portal": "[s1]", "restarting": "[s1]", "gh pr create": "[s1]", "created #42": "[s1]", "someone else": "[]", "hidden idle": "[]"} {
		if got := hits(t, x, query); fmt.Sprint(got) != want {
			t.Errorf("%q: got %v want %s", query, got, want)
		}
	}
}

func TestLocatorFindsClaudeAndCodexFiles(t *testing.T) {
	claude := t.TempDir()
	codex := t.TempDir()
	cwd := "/home/dev/repos/x"
	direct := filepath.Join(claude, "projects", projectDir(cwd), "sess-1.jsonl")
	moved := filepath.Join(claude, "projects", "-other-dir", "sess-2.jsonl")
	rollout := filepath.Join(codex, "2026", "09", "04", "rollout-2026-09-04T10-00-00-abc.jsonl")
	for _, p := range []string{direct, moved, rollout} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Decoys in older date directories must not win.
	if err := os.MkdirAll(filepath.Join(codex, "2026", "08", "01"), 0o755); err != nil {
		t.Fatal(err)
	}
	l := NewLocator(claude, codex)
	now := time.Now()
	l.now = func() time.Time { return now }

	if tg, ok := l.Target("k1", ToolClaude, cwd, "sess-1"); !ok || tg.Path != direct {
		t.Fatalf("direct: ok=%v %+v", ok, tg)
	}
	if tg, ok := l.Target("k2", ToolClaude, cwd, "sess-2"); !ok || tg.Path != moved {
		t.Fatalf("moved: ok=%v %+v", ok, tg)
	}
	if tg, ok := l.Target("k3", ToolCodex, "", "abc"); !ok || tg.Path != rollout {
		t.Fatalf("codex: ok=%v %+v", ok, tg)
	}
	if tg, ok := l.Target("k4", ToolOpenCode, "", "oc-9"); !ok || tg.AgentID != "oc-9" || tg.Path != "" {
		t.Fatalf("opencode: ok=%v %+v", ok, tg)
	}
	if _, ok := l.Target("k5", ToolClaude, cwd, ""); ok {
		t.Fatal("a row with no conversation id resolved")
	}
	if _, ok := l.Target("k6", ToolClaude, cwd, "../../etc/passwd"); ok {
		t.Fatal("a path-shaped id resolved")
	}

	// A miss is remembered until the TTL passes, then looked for again.
	if _, ok := l.Target("k7", ToolClaude, cwd, "sess-3"); ok {
		t.Fatal("missing file resolved")
	}
	late := filepath.Join(claude, "projects", projectDir(cwd), "sess-3.jsonl")
	if err := os.WriteFile(late, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.Target("k7", ToolClaude, cwd, "sess-3"); ok {
		t.Fatal("miss cache did not hold")
	}
	now = now.Add(missTTL + time.Second)
	if tg, ok := l.Target("k7", ToolClaude, cwd, "sess-3"); !ok || tg.Path != late {
		t.Fatalf("after TTL: ok=%v %+v", ok, tg)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// Two writes inside one tick of the filesystem's clock. tmpfs stamps an mtime
// coarser than it looks -- 56 of 400 consecutive writes to /dev/shm shared one
// on the box this was measured on, against 0 of 400 on the disk -- so a write
// that leaves the size alone reads as a file nobody touched, and the row it
// added is never indexed. Size and mtime alone cannot see it; the tail can.
func TestAWriteInsideOneClockTickIsStillSeen(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "transcript.jsonl")
	// Same length both times, so only the content differs: this is the case
	// an appended byte would have masked.
	if err := os.WriteFile(path, []byte(`{"a":"first "}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := stampOfFile(path)
	// Seeded here, against the first write, the way a pass that had already
	// needed a tail would hold one. Reading it after the second write would
	// hash the new bytes and prove nothing.
	before.tail, before.tailRead = tailOf(path, before.size)
	if !before.tailRead {
		t.Fatal("could not read the tail of the file just written")
	}
	if err := os.WriteFile(path, []byte(`{"a":"second"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pinned rather than raced: the collision happens about one write in seven
	// on tmpfs and never on the disk, and a guard that only fires on a
	// coincidence is not a guard.
	stamped := time.Unix(0, before.mtime)
	if err := os.Chtimes(path, stamped, stamped); err != nil {
		t.Fatal(err)
	}
	after := stampOfFile(path)
	if after.size != before.size || after.mtime != before.mtime {
		t.Fatalf("size and mtime were meant to be identical: %+v vs %+v", before, after)
	}
	same, _ := after.unchanged(path, before)
	if same {
		t.Error("a rewritten file read as unchanged; a transcript would stop being searchable")
	}

	// And the other half: a file nobody touched still reads as unchanged, or
	// every pass would re-index everything.
	third := stampOfFile(path)
	_, settled := third.unchanged(path, after)
	if same, _ := stampOfFile(path).unchanged(path, settled); !same {
		t.Error("an untouched file read as changed")
	}
}

func toolUseLine(id, command string) string {
	line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": command}},
	}}})
	return string(line)
}

func toolResultLine(id, output string) string {
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": output},
	}}})
	return string(line)
}

// A session is linked to the pull requests it opened, not the ones it talked
// about. The evidence is the PR-creating command and, paired by id, the URL
// its result printed; a URL in prose or in some other command's output is a
// mention and stays out.
func TestCreatedPullRequestsComeFromTheCreatingCallAndItsResult(t *testing.T) {
	x := openTest(t, Options{})
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path,
		userLine("look at https://github.com/example-org/component-b/pull/1339 first"),
		toolUseLine("t1", "gh pr list --repo example-org/component-b"),
		toolResultLine("t1", "#1353 open https://github.com/example-org/component-b/pull/1353"),
		toolUseLine("t2", "cd /repo && /skills/create-pr/scripts/gh-pr-create.sh --title x --body-file b.md"),
	)
	targets := []Target{{Key: "k", Tool: ToolClaude, Path: path}}
	refreshAll(t, x, targets)
	if got := x.Created("k"); got != "" {
		t.Fatalf("nothing has been opened yet, got %q", got)
	}

	// The result lands in a later append, as it does while a session streams.
	writeLines(t, path,
		toolResultLine("t2", "gh-pr-create: labeled PR #1392 as 'claude'\nhttps://github.com/example-org/component-b/pull/1392\nShell cwd was reset"),
		toolUseLine("t3", "gh pr create --fill"),
		toolResultLine("t3", "https://github.com/o/r/pull/7\n"),
	)
	refreshAll(t, x, targets)
	if got, want := x.Created("k"), "https://github.com/example-org/component-b/pull/1392\nhttps://github.com/o/r/pull/7\n"; got != want {
		t.Errorf("created = %q, want %q", got, want)
	}
	if x.Created("absent") != "" {
		t.Error("an unknown key has opened nothing")
	}
}

func TestCreatedPullRequestsOutliveTheTextTrim(t *testing.T) {
	x := openTest(t, Options{SessionBytes: 4 << 10})
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path,
		toolUseLine("t1", "gh-pr-create.sh --title x"),
		toolResultLine("t1", "https://github.com/o/r/pull/1"),
	)
	var filler []string
	for i := 0; i < 40; i++ {
		filler = append(filler, userLine(strings.Repeat("padding ", 40)))
	}
	writeLines(t, path, filler...)
	targets := []Target{{Key: "k", Tool: ToolClaude, Path: path}}
	refreshAll(t, x, targets)

	if hits(t, x, "gh-pr-create") != nil {
		t.Fatal("the text should have been trimmed past the creating call")
	}
	if got := x.Created("k"); got != "https://github.com/o/r/pull/1\n" {
		t.Errorf("created = %q, want the pull request the trim removed from the text", got)
	}
}

func TestCodexCreatedPullRequests(t *testing.T) {
	x := openTest(t, Options{})
	path := filepath.Join(t.TempDir(), "rollout-2026-09-04T10-00-00-abc.jsonl")
	call, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "function_call", "name": "shell", "call_id": "c1",
		"arguments": `{"command":["bash","-lc","./skills/create-pr/scripts/gh-pr-create.sh --title x"]}`,
	}})
	out, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "function_call_output", "call_id": "c1", "output": "https://github.com/o/r/pull/9\n",
	}})
	other, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "function_call_output", "call_id": "c2", "output": "https://github.com/o/r/pull/10\n",
	}})
	custom, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "custom_tool_call", "name": "exec", "call_id": "c3",
		"input": `text(await tools.exec_command({cmd:"./skills/create-pr/scripts/gh-pr-create.sh --title x"}));`,
	}})
	customOut, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "custom_tool_call_output", "call_id": "c3", "output": "https://github.com/o/r/pull/11\n",
	}})
	writeLines(t, path, string(call), string(out), string(other), string(custom), string(customOut))
	refreshAll(t, x, []Target{{Key: "k", Tool: ToolCodex, Path: path}})
	if got := x.Created("k"); got != "https://github.com/o/r/pull/9\nhttps://github.com/o/r/pull/11\n" {
		t.Errorf("created = %q, want only the pull request the creating call returned", got)
	}
}

func TestCodexWrapperCreationThroughPolling(t *testing.T) {
	for _, mode := range []string{"function", "custom", "wait"} {
		t.Run(mode, func(t *testing.T) {
			custom := mode == "custom"
			x := openTest(t, Options{})
			path := filepath.Join(t.TempDir(), "poll.jsonl")
			row := func(kind, id, name, input string) string {
				p := map[string]any{"type": kind, "call_id": id, "name": name}
				switch kind {
				case "custom_tool_call":
					p["input"] = input
				case "function_call":
					p["arguments"] = input
				default:
					envelope, _ := json.Marshal(map[string]any{"exit_code": 0, "output": input})
					p["output"] = []map[string]string{{"type": "input_text", "text": string(envelope)}}
				}
				b, _ := json.Marshal(map[string]any{"type": "response_item", "payload": p})
				return string(b)
			}
			callType, outType, name := "function_call", "function_call_output", "exec_command"
			create := `{"cmd":"/skills/create-pr/scripts/gh-pr-create.sh --title fix"}`
			poll, pollName := `{"session_id":35048}`, "write_stdin"
			if mode == "wait" {
				poll, pollName = `{"cell_id":"cell-1"}`, "wait"
			}
			if custom {
				callType, outType, name = "custom_tool_call", "custom_tool_call_output", "exec"
				create = `text(await tools.exec_command({cmd:"/skills/create-pr/scripts/gh-pr-create.sh --title fix"}));`
				poll = `text(await tools.write_stdin({session_id:35048,chars:""}));`
				pollName = "exec"
			}
			targets := []Target{{Key: "k", Tool: ToolCodex, Path: path}}
			writeLines(t, path, row(callType, "create", name, create), row(outType, "create", "", `{"session_id":35048}`))
			refreshAll(t, x, targets)
			if len(x.sessions["k"].opening) != 0 {
				t.Fatal("completed calls without URLs must not remain pending")
			}
			writeLines(t, path, row(callType, "poll", pollName, poll))
			refreshAll(t, x, targets)
			writeLines(t, path, row(outType, "poll", "", "gh-pr-create: labeled PR #42 as 'codex'\nhttps://github.com/o/r/pull/42\nhttps://github.com/o/r/pull/99\n"))
			refreshAll(t, x, targets)
			if got := x.Created("k"); got != "https://github.com/o/r/pull/42\n" {
				t.Fatalf("created=%q", got)
			}
			writeLines(t, path, row(callType, "other", pollName, poll), row(outType, "other", "", "https://github.com/o/r/pull/100\ngh-pr-create: labeled PR #1 as 'codex'\nhttps://github.com/o/r/pull/2\n"))
			refreshAll(t, x, targets)
			if got := x.Created("k"); got != "https://github.com/o/r/pull/42\n" {
				t.Fatalf("unrelated result attached: %q", got)
			}
			if custom {
				writeLines(t, path,
					row(callType, "mixed", name, poll+create),
					row(outType, "mixed", "", "gh-pr-create: labeled PR #43 as 'codex'\nhttps://github.com/o/r/pull/43\nhttps://github.com/o/r/pull/99\n"),
				)
				refreshAll(t, x, targets)
				if got := x.Created("k"); got != "https://github.com/o/r/pull/42\nhttps://github.com/o/r/pull/43\n" {
					t.Fatalf("mixed poll and creation attached unrelated output: %q", got)
				}
			}
		})
	}
}
