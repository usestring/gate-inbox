package piext

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The plugin omp loads holds the launch's own server entry, token and all,
// under the name OmpToolPrefix spells, beside the launch's config and out of
// any other user's reach.
func TestEnsureOmpPluginCarriesTheLaunchsServer(t *testing.T) {
	keyDir := t.TempDir()
	mcpConfig := filepath.Join(keyDir, "mcp-claude-abcd1234.json")
	entry := `{"type":"http","url":"http://127.0.0.1:1/mcp/all","headers":{"Authorization":"Bearer secret"},"timeout":600000}`
	if err := os.WriteFile(mcpConfig, []byte(`{"mcpServers":{"gate-inbox":`+entry+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(t.TempDir(), "skills")
	dir, err := EnsureOmpPlugin(mcpConfig, "abcd1234", skills)
	if err != nil {
		t.Fatal(err)
	}
	if dir != OmpPluginPath(mcpConfig, "abcd1234") || filepath.Dir(dir) != keyDir {
		t.Fatalf("plugin at %s, want it beside %s", dir, mcpConfig)
	}
	var manifest struct{ Name string }
	readJSON(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), &manifest)
	var servers struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	readJSON(t, filepath.Join(dir, ".mcp.json"), &servers)
	var got bytes.Buffer
	raw, ok := servers.MCPServers[ompServerName]
	if ok {
		_ = json.Compact(&got, raw)
	}
	if !ok || len(servers.MCPServers) != 1 || got.String() != entry {
		t.Fatalf(".mcp.json servers = %s", servers.MCPServers)
	}
	// omp's name for a plugin server's tool: mcp__ + <plugin>:<server>
	// with every character outside [a-z0-9_] an underscore, then _<tool>.
	prefix := "mcp__" + strings.NewReplacer("-", "_", ":", "_").Replace(manifest.Name+":"+ompServerName) + "_"
	if prefix != OmpToolPrefix {
		t.Errorf("the plugin names tools %s*, OmpToolPrefix says %s*", prefix, OmpToolPrefix)
	}
	if !strings.Contains(string(Source()), `"`+OmpToolPrefix+`rename"`) {
		t.Error("the extension mirrors omp's rename under another name")
	}
	if info, err := os.Stat(filepath.Join(dir, ".mcp.json")); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Errorf(".mcp.json mode %v, %v: the caller token is readable by others", info.Mode().Perm(), err)
	}
	if target, err := os.Readlink(filepath.Join(dir, "skills")); err != nil || target != skills {
		t.Errorf("skills link = %q, %v", target, err)
	}
	if again, err := EnsureOmpPlugin(mcpConfig, "abcd1234", skills); err != nil || again != dir {
		t.Fatalf("second EnsureOmpPlugin = %s, %v", again, err)
	}
}

func TestEnsureOmpPluginRefusesAConfigWithoutTheBoard(t *testing.T) {
	mcpConfig := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(mcpConfig, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureOmpPlugin(mcpConfig, "abcd1234", "/nowhere"); err == nil {
		t.Fatal("a config with no board server made a plugin")
	}
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// The Claude Code servers' plugin is private to the operator, since an
// entry can carry a credential, and names each server as the gate reads it.
func TestWriteClaudeMCPIsPrivate(t *testing.T) {
	keyDir := t.TempDir()
	servers := map[string]json.RawMessage{"web": json.RawMessage(`{"url":"https://example.test","headers":{"Authorization":"Bearer x"}}`)}
	if err := WriteClaudeMCP(keyDir, "abcd1234", servers); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		ClaudeMCPDir(keyDir, "abcd1234"):    0o700,
		ClaudeMCPConfig(keyDir, "abcd1234"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v, %v; want %v", path, info.Mode().Perm(), err, want)
		}
	}
	raw, _ := os.ReadFile(ClaudeMCPConfig(keyDir, "abcd1234"))
	var got struct{ MCPServers map[string]json.RawMessage }
	if err := json.Unmarshal(raw, &got); err != nil || got.MCPServers["web"] == nil {
		t.Fatalf("wrote %s", raw)
	}
	if name, ok := ClaudeMCPServer(ClaudeMCPPlugin + ":web"); !ok || name != "web" {
		t.Errorf("ClaudeMCPServer = %q, %v", name, ok)
	}
	if _, ok := ClaudeMCPServer(OmpServer); ok {
		t.Error("the board's own server read as a Claude Code one")
	}
}
