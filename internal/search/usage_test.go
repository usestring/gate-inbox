package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTokenUsage(t *testing.T) {
	codex := func(tokens, total, capacity int) string {
		return fmt.Sprintf(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":%d},"total_token_usage":{"total_tokens":%d},"model_context_window":%d}}}`, tokens, total, capacity)
	}
	for _, tc := range []struct {
		name, tool, data string
		want             TokenUsage
	}{
		{"claude cache and output", ToolClaude, `{"type":"assistant","timestamp":"2026-10-01T12:00:00Z","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":2000,"cache_creation_input_tokens":300,"output_tokens":40}}}`, TokenUsage{Tokens: 2440, Known: true}},
		{"codex current not cumulative", ToolCodex, codex(12000, 800000, 200000), TokenUsage{Tokens: 12000, Capacity: 200000, Known: true}},
		{"codex latest after compaction", ToolCodex, codex(100000, 800000, 200000) + "\n" + codex(12000, 812000, 200000), TokenUsage{Tokens: 12000, Capacity: 200000, Known: true}},
		{"codex null and partial tail", ToolCodex, codex(12000, 800000, 200000) + `
{"type":"event_msg","payload":{"type":"token_count","info":null}}` + `
{"type":`, TokenUsage{Tokens: 12000, Capacity: 200000, Known: true}},
		{"codex older than initial tail", ToolCodex, codex(12000, 800000, 200000) + "\n" + strings.Repeat(`{"type":"tool"}`+"\n", 30000), TokenUsage{Tokens: 12000, Capacity: 200000, Known: true}},
		{"unknown", ToolCodex, `{"type":"response_item"}`, TokenUsage{}},
		{"negative", ToolCodex, codex(-1, 20, 200000), TokenUsage{}},
		{"unsupported", ToolOpenCode, codex(100, 200, 300), TokenUsage{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			got := ReadTokenUsage(Target{Tool: tc.tool, Path: path})
			if got != tc.want {
				t.Fatalf("usage = %+v, want %+v", got, tc.want)
			}
		})
	}
}
