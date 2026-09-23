package search

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic corpus benchmarks. The shape mimics a Claude Code transcript as
// measured on the operator's machine: a few prose turns per tool call, tool
// results that range from a line to hundreds of KB, and progress rows that
// carry nothing. Run with:
//
//	go test ./internal/search -run '^$' -bench . -benchmem
//
// The real-corpus benchmark in realcorpus_bench_test.go is the number that
// matters; these isolate the parser and the write path from disk shape.

var benchWords = strings.Fields(`the session deploy collector proxy zone rollout restart
	terraform apply plan workstation dashboard alert parser cache scheduler queue
	service worker solver fingerprint transcript index trigram sqlite budget pass
	worktree submodule branch merge squash commit signed review linear ticket slack`)

type corpusShape struct {
	// sessions × bytesPer is the corpus size.
	sessions int
	bytesPer int
	// hugeEvery inserts a 300KB tool result every N tool calls; 0 for none.
	hugeEvery int
}

func genSession(rng *rand.Rand, dir, name string, shape corpusShape) string {
	path := filepath.Join(dir, name+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	written, calls := 0, 0
	sentence := func(n int) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			if i > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(benchWords[rng.Intn(len(benchWords))])
		}
		return sb.String()
	}
	for written < shape.bytesPer {
		rows := []any{
			map[string]any{"type": "user", "uuid": name, "message": map[string]any{"role": "user", "content": sentence(12)}},
			map[string]any{"type": "assistant", "uuid": name, "message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": sentence(60)},
				map[string]any{"type": "text", "text": sentence(40)},
				map[string]any{"type": "tool_use", "id": "t", "name": "Bash", "input": map[string]any{"command": "go test ./" + sentence(2), "description": sentence(4)}},
			}}},
			map[string]any{"type": "progress", "data": map[string]any{"type": "hook", "text": sentence(5)}},
		}
		calls++
		resultLen := 200 + rng.Intn(6000)
		if shape.hugeEvery > 0 && calls%shape.hugeEvery == 0 {
			resultLen = 300 << 10
		}
		rows = append(rows, map[string]any{"type": "user", "uuid": name, "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t", "content": strings.Repeat(sentence(8)+"\n", resultLen/50+1)},
		}}})
		for _, row := range rows {
			before, _ := f.Seek(0, 1)
			if err := enc.Encode(row); err != nil {
				panic(err)
			}
			after, _ := f.Seek(0, 1)
			written += int(after - before)
		}
	}
	return path
}

func genCorpus(b *testing.B, shape corpusShape) ([]Target, int64) {
	b.Helper()
	dir := b.TempDir()
	rng := rand.New(rand.NewSource(1))
	var targets []Target
	var total int64
	for i := 0; i < shape.sessions; i++ {
		path := genSession(rng, dir, fmt.Sprintf("s%02d", i), shape)
		info, _ := os.Stat(path)
		total += info.Size()
		targets = append(targets, Target{Key: fmt.Sprintf("s%02d", i), Tool: ToolClaude, Path: path, AgentID: fmt.Sprintf("s%02d", i)})
	}
	return targets, total
}

func coldIndex(b *testing.B, x *Index, targets []Target, budget int) (passes int) {
	b.Helper()
	for {
		p, err := x.Refresh(targets, budget)
		if err != nil {
			b.Fatal(err)
		}
		passes++
		if p.Done {
			return passes
		}
	}
}

// BenchmarkParseLine is the parser alone: the cost of one transcript row of
// each kind, which is what a cold index is made of.
func BenchmarkParseLine(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	dir := b.TempDir()
	path := genSession(rng, dir, "s", corpusShape{bytesPer: 2 << 20, hugeEvery: 20})
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			LineText(ToolClaude, []byte(line), DefaultLimits)
		}
	}
}

// BenchmarkColdIndex builds a fleet's index from nothing. Bytes/s is
// transcript bytes consumed, which is what a cold start is bounded by.
func BenchmarkColdIndex(b *testing.B) {
	for _, shape := range []corpusShape{
		{sessions: 10, bytesPer: 1 << 20, hugeEvery: 0},
		{sessions: 10, bytesPer: 1 << 20, hugeEvery: 10},
		{sessions: 50, bytesPer: 2 << 20, hugeEvery: 10},
	} {
		b.Run(fmt.Sprintf("sessions=%d,MB=%d,huge=%d", shape.sessions, shape.bytesPer>>20, shape.hugeEvery), func(b *testing.B) {
			targets, total := genCorpus(b, shape)
			b.SetBytes(total)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				x := New(Options{})
				b.StartTimer()
				passes := coldIndex(b, x, targets, 8<<20)
				b.StopTimer()
				st := x.Stats()
				b.ReportMetric(float64(passes), "passes")
				b.ReportMetric(float64(st.Bytes)/float64(total), "index/corpus")
				x.Close()
				b.StartTimer()
			}
		})
	}
}

// BenchmarkIdlePass is the steady state: every file unchanged, one stat each.
func BenchmarkIdlePass(b *testing.B) {
	targets, _ := genCorpus(b, corpusShape{sessions: 50, bytesPer: 64 << 10})
	x := New(Options{})
	defer x.Close()
	coldIndex(b, x, targets, 8<<20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if p, err := x.Refresh(targets, 8<<20); err != nil || p.Changed {
			b.Fatalf("idle pass changed: %+v %v", p, err)
		}
	}
}

// BenchmarkAppendPass is a live board: a few sessions each wrote one turn
// since the last pass.
func BenchmarkAppendPass(b *testing.B) {
	targets, _ := genCorpus(b, corpusShape{sessions: 50, bytesPer: 256 << 10})
	x := New(Options{})
	defer x.Close()
	coldIndex(b, x, targets, 8<<20)
	line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "text", "text": "appended turn about the collector rollout"},
	}}})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for _, t := range targets[:5] {
			f, _ := os.OpenFile(t.Path, os.O_APPEND|os.O_WRONLY, 0o644)
			f.Write(append(line, '\n'))
			f.Close()
		}
		b.StartTimer()
		p, err := x.Refresh(targets, 8<<20)
		if err != nil || p.Rows != 5 {
			b.Fatalf("append pass: %+v %v", p, err)
		}
	}
}

// BenchmarkSearch is one keystroke's query against a 50-session index.
func BenchmarkSearch(b *testing.B) {
	targets, _ := genCorpus(b, corpusShape{sessions: 50, bytesPer: 1 << 20, hugeEvery: 10})
	x := New(Options{})
	defer x.Close()
	coldIndex(b, x, targets, 8<<20)
	for _, query := range []string{"collector rollout", "sample-run", "go test ./", "xyzzy-absent"} {
		b.Run(query, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, _, err := x.Search(query, 200); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
