package snippets

import (
	"os"
	"reflect"
	"testing"
)

func TestLaunchFileIsIndependentAndKeepsPromptsAndNumericKeys(t *testing.T) {
	dir := t.TempDir()
	ordinary := []byte(`[{"key":"y","text":"yes"}]`)
	launches := []byte(`[{"key":"1","text":"Clear disk space"},{"key":"Y","text":"Review changes\nKeep it short"},{"key":"1","text":"duplicate"},{"key":"bad","text":"bad key"},{"key":"f","text":"--bad-flag"}]`)
	if err := os.WriteFile(Path(dir), ordinary, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LaunchPath(dir), launches, 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := LoadLaunch(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Snippet{{Key: "1", Text: "Clear disk space"}, {Key: "y", Text: "Review changes\nKeep it short"}}
	if !reflect.DeepEqual(set.Snippets, want) || len(set.Problems) != 3 {
		t.Fatalf("launches = %+v, problems = %v", set.Snippets, set.Problems)
	}
	sent, err := Load(dir)
	if err != nil || len(sent.Snippets) != 1 || sent.Snippets[0].Text != "yes" {
		t.Fatalf("ordinary snippets changed: %+v, %v", sent, err)
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil || string(raw) != string(ordinary) {
		t.Fatal("launch loading overwrote ordinary snippets")
	}
	if err := os.WriteFile(LaunchPath(dir), []byte(`[`), 0o600); err != nil {
		t.Fatal(err)
	}
	broken, err := LoadLaunch(dir)
	if err == nil || len(broken.Snippets) != 0 {
		t.Fatal("a malformed launch file silently bound defaults")
	}
}
