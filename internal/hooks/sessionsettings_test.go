package hooks

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/grant"
)

func TestSessionSettingsCarryHooksAndGrants(t *testing.T) {
	m := NewManager(t.TempDir())
	if got := m.LaunchSettingsPath("abc12345"); got != m.SettingsPath() {
		t.Fatalf("ungranted launch carries %s, want the shared file", got)
	}
	path, err := m.WriteSessionSettings("abc12345", []grant.Grant{{Kind: grant.KindUnsandboxed, Value: "tools/x.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.LaunchSettingsPath("abc12345"); got != path {
		t.Fatalf("granted launch carries %s, want %s", got, path)
	}
	if got := m.LaunchSettingsPath("other123"); got != m.SettingsPath() {
		t.Fatalf("another session's launch carries %s, want the shared file", got)
	}
	settings := readJSON(t, path)
	if _, ok := settings["hooks"].(map[string]any)["Stop"]; !ok {
		t.Fatalf("session file has no hooks: %v", settings)
	}
	if allow := settings["permissions"].(map[string]any)["allow"]; len(allow.([]any)) != 1 {
		t.Fatalf("allow = %v", allow)
	}
	for _, launched := range []string{m.SettingsPath(), path} {
		if !strings.Contains(SettingsArgv(launched), m.SettingsArgvMark()) {
			t.Fatalf("argv for %s does not carry the mark %q", launched, m.SettingsArgvMark())
		}
	}

	// A refresh replaces the hooks and keeps the grants.
	settings["hooks"] = map[string]any{"stale": true}
	raw, _ := json.Marshal(settings)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshSessionSettings("abc12345"); err != nil {
		t.Fatal(err)
	}
	refreshed := readJSON(t, path)
	if _, stale := refreshed["hooks"].(map[string]any)["stale"]; stale {
		t.Fatal("refresh kept the stale hooks")
	}
	if refreshed["sandbox"].(map[string]any)["excludedCommands"].([]any)[0] != "tools/x.sh" {
		t.Fatalf("refresh lost the grant: %v", refreshed)
	}

	if _, err := m.WriteSessionSettings("abc12345", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no grants left the file behind: %v", err)
	}
	if got := m.LaunchSettingsPath("abc12345"); got != m.SettingsPath() {
		t.Fatalf("revoked launch carries %s, want the shared file", got)
	}
}

func TestSessionSettingsRefuseAPathForAnID(t *testing.T) {
	m := NewManager(t.TempDir())
	for _, id := range []string{"", "../x", "a/b", "a.b"} {
		if _, err := m.WriteSessionSettings(id, []grant.Grant{{Kind: grant.KindDomain, Value: "example.com"}}); err == nil {
			t.Errorf("WriteSessionSettings(%q) wrote a file", id)
		}
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
