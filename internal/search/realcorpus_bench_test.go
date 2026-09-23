package search

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The real-corpus benchmark indexes the operator's own transcripts: the N
// most recently written under ~/.claude/projects, standing in for a board of
// N sessions. A synthetic corpus has whatever shape its author guessed; the
// cost being chased here is a function of what real sessions write.
//
// Opt in, because it reads private files and takes seconds:
//
//	GATE_SEARCH_BENCH_SESSIONS=55 go test ./internal/search -run TestRealCorpus -v
//
// GATE_SEARCH_BENCH_BUDGET_MB sets the per-pass budget (default 8).
// GATE_SEARCH_BENCH_LIMITS="16,2,4" sets the text, input and result caps in KB;
// GATE_SEARCH_BENCH_SESSION_MB the per-session cap.

func realTargets(tb testing.TB) []Target {
	tb.Helper()
	raw := os.Getenv("GATE_SEARCH_BENCH_SESSIONS")
	if raw == "" {
		tb.Skip("set GATE_SEARCH_BENCH_SESSIONS to run against the real corpus")
	}
	want, err := strconv.Atoi(raw)
	if err != nil || want <= 0 {
		tb.Skipf("GATE_SEARCH_BENCH_SESSIONS=%q is not a count", raw)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		tb.Skip("no home directory")
	}
	projects := filepath.Join(home, ".claude", "projects")
	type file struct {
		path  string
		mtime time.Time
	}
	var files []file
	dirs, _ := os.ReadDir(projects)
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(projects, dir.Name()))
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".jsonl" {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, file{filepath.Join(projects, dir.Name(), entry.Name()), info.ModTime()})
		}
	}
	if len(files) == 0 {
		tb.Skip("no Claude Code corpus on this machine")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.After(files[j].mtime) })
	if len(files) > want {
		files = files[:want]
	}
	var targets []Target
	for i, f := range files {
		targets = append(targets, Target{Key: fmt.Sprintf("row-%02d", i), Tool: ToolClaude, Path: f.path})
	}
	return targets
}

func TestRealCorpus(t *testing.T) {
	targets := realTargets(t)
	budget := 8 << 20
	if raw := os.Getenv("GATE_SEARCH_BENCH_BUDGET_MB"); raw != "" {
		if mb, err := strconv.Atoi(raw); err == nil && mb > 0 {
			budget = mb << 20
		}
	}
	var corpus int64
	for _, tg := range targets {
		info, err := os.Stat(tg.Path)
		if err != nil {
			t.Fatal(err)
		}
		corpus += info.Size()
	}
	opts := Options{}
	if raw := os.Getenv("GATE_SEARCH_BENCH_LIMITS"); raw != "" {
		var kb [3]int
		for i, part := range strings.SplitN(raw, ",", 3) {
			kb[i], _ = strconv.Atoi(part)
		}
		opts.Limits = Limits{Text: kb[0] << 10, Input: kb[1] << 10, Result: kb[2] << 10}
	}
	if raw := os.Getenv("GATE_SEARCH_BENCH_SESSION_MB"); raw != "" {
		if mb, err := strconv.Atoi(raw); err == nil && mb > 0 {
			opts.SessionBytes = mb << 20
		}
	}
	x := New(opts)
	defer x.Close()

	start := time.Now()
	var passes, rows int
	var slowest time.Duration
	for {
		p, err := x.Refresh(targets, budget)
		if err != nil {
			t.Fatal(err)
		}
		passes++
		rows += p.Rows
		slowest = max(slowest, p.Elapsed)
		if p.Done {
			break
		}
	}
	cold := time.Since(start)
	st := x.Stats()
	t.Logf("cold: %d sessions, %.0fMB corpus, %d rows, %d passes of %dMB, %v total (%.0f MB/s), slowest pass %v",
		len(targets), float64(corpus)/1e6, rows, passes, budget>>20, cold.Round(time.Millisecond),
		float64(corpus)/1e6/cold.Seconds(), slowest.Round(time.Millisecond))
	t.Logf("index: %.1fMB of text + %.1fMB of filters in memory, %.1f%% of corpus, %d turns", float64(st.Bytes)/1e6, float64(st.Filters)/1e6, 100*float64(st.Bytes+st.Filters)/float64(corpus), st.Turns)

	start = time.Now()
	p, err := x.Refresh(targets, budget)
	if err != nil {
		t.Fatal(err)
	}
	// A live session in the set can append between passes; that is a fact
	// about the corpus, not a failure.
	t.Logf("idle pass: %v (changed=%v)", time.Since(start), p.Changed)

	// Each query runs many times; the first call in a process pays for
	// goroutine and cache warm-up that a keystroke never sees, so the
	// numbers that matter are the median and the floor. The narrowing cache
	// is defeated between runs by alternating with an unrelated query.
	const runs = 40
	for _, query := range []string{"terraform", "go test", "http-gateway", "session_id", "zzqx-nothing", "db-*-07"} {
		var samples []time.Duration
		var hits int
		for i := 0; i < runs; i++ {
			x.Search("qqqq-warm", 200)
			start = time.Now()
			keys, ok, err := x.Search(query, 200)
			samples = append(samples, time.Since(start))
			if err != nil || !ok {
				t.Fatal(err)
			}
			hits = len(keys)
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		t.Logf("query %-14q %3d hits  min %v  median %v  p90 %v", query, hits,
			samples[0].Round(10*time.Microsecond), samples[runs/2].Round(10*time.Microsecond), samples[runs*9/10].Round(10*time.Microsecond))
	}
}
