package mcpreg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// A claude pointed at the shared server gets an http entry whose headers
// name its session by reference, so the config file every session shares
// holds no session's id or token.
func TestClaudeSharedConfigNamesTheSessionByReference(t *testing.T) {
	dir := t.TempDir()
	command, err := ApplyProfile("claude", "/opt/bin/gate-inbox", dir, "claude",
		map[string]string{hooks.EnvSessionID: "abcd1234"}, "", "core", "http://127.0.0.1:4567", false)
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(command, "--mcp-config ")
	if !ok {
		t.Fatalf("no --mcp-config in %q", command)
	}
	path := strings.Trim(strings.Fields(after)[0], "'")
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Command string            `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	entry := config.MCPServers[ServerName]
	if entry.Type != "http" || entry.URL != "http://127.0.0.1:4567/mcp/core" || entry.Command != "" {
		t.Fatalf("entry = %+v, want an http entry for the core profile", entry)
	}
	if entry.Headers["X-Gate-Inbox-Session"] != "${GATE_INBOX_SESSION_ID}" || entry.Headers["X-Gate-Inbox-Token"] != "${GATE_INBOX_SESSION_TOKEN}" {
		t.Fatalf("headers = %v, want references to the session's own environment", entry.Headers)
	}
	if strings.Contains(string(data), "abcd1234") {
		t.Fatal("the shared config names a session")
	}
}
