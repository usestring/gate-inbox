package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsCarryExtensionHooksAfterTheBoardsOwn(t *testing.T) {
	defer UseExtensionHooks([]ExtensionHook{
		{ID: "policy", Event: "PreToolUse", Matcher: "mcp__slack__conversations_add_message"},
		{ID: "notes", Event: "Stop"},
	})()
	content, err := settingsContent("/keys")
	if err != nil {
		t.Fatal(err)
	}
	var parsed settingsFile
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	pre := parsed.Hooks["PreToolUse"]
	if len(pre) != 3 || pre[0].Matcher != "*" || pre[1].Matcher != blockingTool {
		t.Fatalf("PreToolUse = %+v, want the board's two entries first", pre)
	}
	last := pre[2]
	if last.Matcher != "mcp__slack__conversations_add_message" || len(last.Hooks) != 1 ||
		last.Hooks[0].Command != hookCommandLine("ext policy PreToolUse") {
		t.Fatalf("extension entry = %+v", last)
	}
	stop := parsed.Hooks["Stop"]
	if len(stop) != 2 || stop[1].Matcher != "" || stop[1].Hooks[0].Command != hookCommandLine("ext notes Stop") {
		t.Fatalf("Stop = %+v", stop)
	}
}

func TestSettingsWithoutExtensionHooksAreUnchanged(t *testing.T) {
	before, err := settingsContent("/keys")
	if err != nil {
		t.Fatal(err)
	}
	restore := UseExtensionHooks([]ExtensionHook{{ID: "policy", Event: "PreToolUse", Matcher: "Bash"}})
	restore()
	after, err := settingsContent("/keys")
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("restoring the extension hooks left the settings changed")
	}
}

// An adopted session runs the launch's hooks through DispatchGlobal, so an
// extension's hook reaches it too, and its output is the hook's.
func TestDispatchGlobalRunsAnExtensionHook(t *testing.T) {
	defer UseExtensionHooks([]ExtensionHook{{ID: "policy", Event: "PreToolUse", Matcher: "mcp__slack__conversations_add_message"}})()
	configDir := t.TempDir()
	m := NewManager(configDir)
	bin := filepath.Join(t.TempDir(), "gate-inbox")
	script := "#!/bin/sh\n[ \"$1 $2 $3 $4\" = \"hook ext policy PreToolUse\" ] || exit 0\n" +
		"printf '{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"updatedInput\":{\"text\":\"signed\"}}}\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvSessionID, "adopted1")
	t.Setenv(EnvStatusFile, m.StatusFile("adopted1"))
	t.Setenv(EnvExecutable, bin)
	if err := os.MkdirAll(m.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	out := m.DispatchGlobal("PreToolUse", []byte(`{"tool_name":"mcp__slack__conversations_add_message"}`))
	if !strings.Contains(out, `"updatedInput":{"text":"signed"}`) {
		t.Fatalf("matching tool printed %q", out)
	}
	if out := m.DispatchGlobal("PreToolUse", []byte(`{"tool_name":"Bash"}`)); out != "" {
		t.Fatalf("another tool printed %q", out)
	}
}
