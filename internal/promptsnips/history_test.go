package promptsnips

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadHistoryCountsEachToolSubmissionOnce(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	codexHome := filepath.Join(root, "codex")
	for _, dir := range []string{claudeHome, codexHome} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Hour)
	claude := fmt.Sprintf("{\"display\":\"Review the pull request\",\"timestamp\":%d}\n", at.UnixMilli())
	codex := fmt.Sprintf("{\"text\":\"Review the pull request\",\"ts\":%d}\n", at.Unix())
	for path, content := range map[string]string{
		filepath.Join(claudeHome, "history.jsonl"): claude + claude + "not json\n" + claude,
		filepath.Join(codexHome, "history.jsonl"):  codex,
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	subs, err := ReadHistory(claudeHome, codexHome, at.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 4 {
		t.Fatalf("read %d submissions, want 4", len(subs))
	}
	if got := Build(subs, time.Now()); len(got) != 1 || got[0].Count != 4 {
		t.Fatalf("counted history as %+v", got)
	}
	if got, err := ReadHistory(claudeHome, filepath.Join(root, "missing"), at.Add(time.Minute)); err != nil || len(got) != 0 {
		t.Fatalf("old rows or missing Codex history: %v, %v", got, err)
	}
}

func TestBuildSkipsHistoryPlaceholders(t *testing.T) {
	for _, placeholder := range []string{"[Pasted text #1 +40 lines]", "See [Image #1]"} {
		if got := Build(subs(placeholder, 4, time.Now()), time.Now()); len(got) != 0 {
			t.Fatalf("suggested placeholder %q: %+v", placeholder, got)
		}
	}
}
