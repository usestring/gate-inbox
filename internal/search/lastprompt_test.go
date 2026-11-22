package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLastOperatorPromptWidensPastToolTraffic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	var b strings.Builder
	b.WriteString(`{"type":"user","message":{"role":"user","content":"first ask"}}` + "\n")
	b.WriteString(`{"type":"user","message":{"role":"user","content":"newest ask"}}` + "\n")
	// Push the prompt out of the first 256KB window with assistant output.
	filler := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + strings.Repeat("x", 4000) + `"}]}}` + "\n"
	for range 100 {
		b.WriteString(filler)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadLastOperatorPrompt(Target{Tool: ToolClaude, Path: path}); got != "newest ask" {
		t.Fatalf("ReadLastOperatorPrompt = %q, want %q", got, "newest ask")
	}
}
