package convo

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPendingToolStringsReadsTheOneUnresolvedCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	lines := `{"type":"user","message":{"role":"user","content":"run it"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"curl -sS https://example.com/","description":"Fetch it"}}]}}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	got := PendingToolStrings(path)
	slices.Sort(got)
	if want := []string{"Fetch it", "curl -sS https://example.com/"}; !slices.Equal(got, want) {
		t.Fatalf("strings = %q, want %q", got, want)
	}

	resolved := lines + `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"ok"}]}}
`
	if err := os.WriteFile(path, []byte(resolved), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := PendingToolStrings(path); got != nil {
		t.Fatalf("strings after the result = %q, want none", got)
	}
}
