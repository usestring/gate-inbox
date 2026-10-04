package agentsession

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexTitlesTakesTheLastNameForEachWantedThread(t *testing.T) {
	index := filepath.Join(t.TempDir(), "session_index.jsonl")
	lines := `{"id":"a","thread_name":"first"}
{"id":"b","thread_name":"other"}
not json
{"id":"a","thread_name":"Guardian review"}
`
	if err := os.WriteFile(index, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := codexTitlesFrom(index, map[string]bool{"a": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["a"] != "Guardian review" {
		t.Fatalf("codexTitlesFrom = %v", got)
	}
}

func TestCodexTitlesWithoutAnIndexIsEmpty(t *testing.T) {
	got, err := codexTitlesFrom(filepath.Join(t.TempDir(), "session_index.jsonl"), map[string]bool{"a": true})
	if err != nil || len(got) != 0 {
		t.Fatalf("codexTitlesFrom = %v, %v", got, err)
	}
}
