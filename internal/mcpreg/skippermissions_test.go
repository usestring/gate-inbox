package mcpreg

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// A session launched with skip permissions on gets a config of its own that
// loads the plugin allowing every request opencode would ask about, and the
// plugin is on disk where that config says.
func TestApplyOpencodeSkipLoadsTheAllowPlugin(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{hooks.EnvSessionID: "abcd1234"}
	command, err := ApplyProfile("opencode", "/opt/bin/gate-inbox", dir, "opencode", env, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if command != "opencode --standalone" {
		t.Fatalf("command = %q, want the skip kept off the command line", command)
	}
	plugin := filepath.Join(dir, hooks.GeneratedName("gate-inbox-opencode-skip-permissions", skipPermissionsJS))
	if want := filepath.Join(dir, hooks.GeneratedName("gate-inbox-mcp-opencode-abcd1234.json", opencodeConfig("/opt/bin/gate-inbox", "", "", plugin))); env["OPENCODE_CONFIG"] != want {
		t.Fatalf("OPENCODE_CONFIG = %q, want the session's own %q", env["OPENCODE_CONFIG"], want)
	}
	content, err := os.ReadFile(env["OPENCODE_CONFIG"])
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Plugins []string       `json:"plugins"`
		MCP     map[string]any `json:"mcp"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Plugins) != 1 || parsed.Plugins[0] != plugin || parsed.MCP == nil {
		t.Fatalf("config must carry the server and load only the skip plugin:\n%s", content)
	}
	// v2 loads a configured local plugin only from a directory's index module.
	index, err := os.ReadFile(filepath.Join(plugin, "index.js"))
	if err != nil || !bytes.Equal(index, skipPermissionsJS) {
		t.Fatalf("plugin index.js = %q, %v; want the embedded plugin", index, err)
	}
	if !strings.Contains(string(index), `ctx.permission.hook("evaluate"`) || !strings.Contains(string(index), `event.effect = "allow"`) {
		t.Fatalf("plugin does not allow through the evaluate hook:\n%s", index)
	}
	// A second launch finds the plugin already written and leaves it be.
	if _, err := ApplyProfile("opencode", "/opt/bin/gate-inbox", dir, "opencode", map[string]string{hooks.EnvSessionID: "ef567890"}, "", "", true); err != nil {
		t.Fatalf("second launch: %v", err)
	}
}

// With skip permissions off the config loads no plugin, so the session keeps
// every prompt its own config and opencode's defaults ask.
func TestApplyOpencodeWithoutSkipLoadsNoPlugin(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{hooks.EnvSessionID: "abcd1234"}
	if _, err := ApplyProfile("opencode", "/opt/bin/gate-inbox", dir, "opencode", env, "", "", false); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(env["OPENCODE_CONFIG"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), `"plugins"`) {
		t.Fatalf("a session with its prompts on loads a plugin:\n%s", content)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "gate-inbox-opencode-skip-permissions-*")); len(matches) != 0 {
		t.Fatalf("the skip plugin was written for a session with its prompts on: %v", matches)
	}
}

func TestPreviewOpencodeSkipWritesNothing(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{hooks.EnvSessionID: "abcd1234"}
	if _, err := PreviewProfile("opencode", "/opt/bin/gate-inbox", dir, "opencode", env, "", "", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env["OPENCODE_CONFIG"], "gate-inbox-mcp-opencode-abcd1234-") {
		t.Fatalf("OPENCODE_CONFIG = %q, want the session's own config named", env["OPENCODE_CONFIG"])
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("a preview wrote %v (err %v)", entries, err)
	}
}

// Only the opencode style generates a config that can turn prompts off; any
// other style refuses rather than launch with the prompts still on.
func TestApplySkipIsRefusedForAStyleWithoutTheConfig(t *testing.T) {
	for _, style := range []string{"claude", "codex", StyleNone} {
		_, err := ApplyProfile(style, "/opt/bin/gate-inbox", t.TempDir(), style, map[string]string{hooks.EnvSessionID: "abcd1234"}, "", "", true)
		if err == nil || !strings.Contains(err.Error(), "permission prompts") {
			t.Errorf("%s: err = %v, want a refusal", style, err)
		}
	}
}
