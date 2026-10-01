package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A session's MCP server keeps the build that started it for weeks, and it
// launches children too. Under one fixed name its launch put its old hooks
// back for the whole board; under a stamped one it can only touch its own.
func TestEnsureSettingsLeavesAnotherBuildsFileAlone(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	if err := os.MkdirAll(m.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"hooks":{"Stop":[]}}`)
	legacy := filepath.Join(m.Dir(), "claude-settings.json")
	oldPath, err := WriteGenerated(m.Dir(), "claude-settings.json", old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, old, 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := m.EnsureSettings()
	if err != nil {
		t.Fatal(err)
	}
	if path == oldPath || path == legacy {
		t.Fatalf("EnsureSettings wrote %s, another build's file", path)
	}
	if path != m.SettingsPath() {
		t.Fatalf("EnsureSettings wrote %s, SettingsPath names %s", path, m.SettingsPath())
	}
	for _, other := range []string{oldPath, legacy} {
		if got, err := os.ReadFile(other); err != nil || string(got) != string(old) {
			t.Fatalf("%s = %q (err %v), want the other build's bytes untouched", other, got, err)
		}
	}
	// The old build launching again rewrites only its own file.
	if again, err := WriteGenerated(m.Dir(), "claude-settings.json", old); err != nil || again != oldPath {
		t.Fatalf("old build wrote %s (err %v), want %s", again, err, oldPath)
	}
	want, err := settingsContent("")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || len(got) == 0 || strings.Contains(string(got), `"Stop":[]`) {
		t.Fatalf("this build's settings = %q (err %v), want its own hooks (%d bytes)", got, err, len(want))
	}
}

func TestGeneratedNameIsTheContents(t *testing.T) {
	a := GeneratedName("claude-settings.json", []byte("a"))
	if a != GeneratedName("claude-settings.json", []byte("a")) {
		t.Fatal("one content named two ways")
	}
	if a == GeneratedName("claude-settings.json", []byte("b")) {
		t.Fatal("two contents share a name")
	}
	if !strings.HasPrefix(a, "claude-settings-") || filepath.Ext(a) != ".json" {
		t.Fatalf("name %q, want claude-settings-<stamp>.json", a)
	}
}

// The poller tells a wired session by its argv; a release must not report
// every session launched before it unwired.
func TestSettingsArgvMarkMatchesEveryBuildsLaunch(t *testing.T) {
	m := NewManager(t.TempDir())
	mark := m.SettingsArgvMark()
	for _, argv := range []string{
		"claude --session-id x " + SettingsArgv(filepath.Join(m.Dir(), "claude-settings.json")),
		"claude --session-id x " + SettingsArgv(m.SettingsPath()),
	} {
		if !strings.Contains(argv, mark) {
			t.Fatalf("argv %q does not carry mark %q", argv, mark)
		}
	}
}
