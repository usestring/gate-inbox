package hooks

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/grant"
)

func TestSessionSettingsCarryGrantsAndNoHooks(t *testing.T) {
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
	if _, ok := settings["hooks"]; ok {
		t.Fatalf("session file carries hooks, which ride the plugin: %v", settings)
	}
	if allow := settings["permissions"].(map[string]any)["allow"]; len(allow.([]any)) != 1 {
		t.Fatalf("allow = %v", allow)
	}
	for _, launched := range []string{m.SettingsPath(), path} {
		if !strings.Contains(SettingsArgv(launched), m.SettingsArgvMark()) {
			t.Fatalf("argv for %s does not carry the mark %q", launched, m.SettingsArgvMark())
		}
	}

	// A refresh drops hooks an older build wrote and keeps the grants.
	settings["hooks"] = map[string]any{"stale": true}
	raw, _ := json.Marshal(settings)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshSessionSettings("abc12345"); err != nil {
		t.Fatal(err)
	}
	refreshed := readJSON(t, path)
	if _, stale := refreshed["hooks"]; stale {
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

// A window alone gives a session its own file; grants written after it keep
// it, a refresh keeps it, and taking both away sends the session back to the
// shared file.
func TestSessionSettingsCarryTheCompactWindow(t *testing.T) {
	m := NewManager(t.TempDir())
	const id = "abc12345"
	path := m.SessionSettingsPath(id)

	if err := m.SetSessionCompactWindow(id, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no window made a file: %v", err)
	}

	if err := m.SetSessionCompactWindow(id, 600000); err != nil {
		t.Fatal(err)
	}
	if got := m.LaunchSettingsPath(id); got != path {
		t.Fatalf("a session with a window launches on %s, want its own %s", got, path)
	}
	if got := readJSON(t, path)["autoCompactWindow"]; got != float64(600000) {
		t.Fatalf("autoCompactWindow = %v, want 600000", got)
	}

	if _, err := m.WriteSessionSettings(id, []grant.Grant{{Kind: grant.KindUnsandboxed, Value: "tools/x.sh"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshSessionSettings(id); err != nil {
		t.Fatal(err)
	}
	settings := readJSON(t, path)
	if settings["autoCompactWindow"] != float64(600000) {
		t.Fatalf("a grant dropped the window: %v", settings)
	}
	if settings["sandbox"].(map[string]any)["excludedCommands"].([]any)[0] != "tools/x.sh" {
		t.Fatalf("the grant is missing: %v", settings)
	}

	if err := m.SetSessionCompactWindow(id, 300000); err != nil {
		t.Fatal(err)
	}
	settings = readJSON(t, path)
	if settings["autoCompactWindow"] != float64(300000) || settings["sandbox"] == nil {
		t.Fatalf("a new window lost it or the grant: %v", settings)
	}

	// The grants go; the window keeps the file.
	if _, err := m.WriteSessionSettings(id, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if readJSON(t, path)["autoCompactWindow"] != float64(300000) || strings.Contains(string(raw), "tools/x.sh") {
		t.Fatalf("after the grants went: %s", raw)
	}

	// And then the window goes, and nothing is left to keep a file for.
	if err := m.SetSessionCompactWindow(id, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a file with nothing left in it was kept: %v", err)
	}
	if got := m.LaunchSettingsPath(id); got != m.SettingsPath() {
		t.Fatalf("launch carries %s, want the shared file", got)
	}
}

// Taking the window out of a file that holds grants keeps the file and the
// grants.
func TestSessionSettingsClearingTheWindowKeepsGrants(t *testing.T) {
	m := NewManager(t.TempDir())
	const id = "abc12345"
	path, err := m.WriteSessionSettings(id, []grant.Grant{{Kind: grant.KindDomain, Value: "example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetSessionCompactWindow(id, 600000); err != nil {
		t.Fatal(err)
	}
	if err := m.SetSessionCompactWindow(id, 0); err != nil {
		t.Fatal(err)
	}
	settings := readJSON(t, path)
	if _, ok := settings["autoCompactWindow"]; ok {
		t.Fatalf("the window was kept: %v", settings)
	}
	if settings["sandbox"] == nil {
		t.Fatalf("the grant went with the window: %v", settings)
	}
}

// The poller calls this every pass, so a window the file already says is
// not written again.
func TestSessionSettingsSameWindowIsNotRewritten(t *testing.T) {
	m := NewManager(t.TempDir())
	const id = "abc12345"
	if err := m.SetSessionCompactWindow(id, 600000); err != nil {
		t.Fatal(err)
	}
	path := m.SessionSettingsPath(id)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetSessionCompactWindow(id, 600000); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A write replaces the file by rename, so it would be a new inode.
	if !os.SameFile(before, after) {
		t.Fatal("an unchanged window was written again")
	}
}

// A launch records the window it carried, and a later rewrite of the
// settings file does not move it: the running process is still on the old one.
func TestLaunchedCompactWindowKeepsWhatTheLaunchCarried(t *testing.T) {
	m := NewManager(t.TempDir())
	if _, ok := m.LaunchedCompactWindow("s1"); ok {
		t.Fatal("a session that never launched has a launched window")
	}
	if err := m.SetSessionCompactWindow("s1", 300000); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordLaunchedCompactWindow("s1"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetSessionCompactWindow("s1", 600000); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.LaunchedCompactWindow("s1"); !ok || got != 300000 {
		t.Fatalf("launched window = %d, %v; want 300000", got, ok)
	}
}
