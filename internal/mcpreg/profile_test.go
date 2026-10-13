package mcpreg

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// A core launch starts the same server under the same name with only the
// profile flag added, so every mcp__gate-inbox__* tool name and permission
// rule it keeps reads as before; a launch with no profile is unchanged.
func TestApplyProfileStartsTheCoreServer(t *testing.T) {
	for _, c := range []struct {
		profile string
		want    []string
	}{
		{"", []string{"mcp"}},
		{ProfileCore, []string{"mcp", "--profile", "core"}},
	} {
		dir := t.TempDir()
		command, err := ApplyProfile("claude", "/opt/bin/gate-inbox", dir, "claude", map[string]string{hooks.EnvSessionID: "abcd1234"}, "", c.profile)
		if err != nil {
			t.Fatal(err)
		}
		path := strings.Trim(strings.Fields(strings.SplitAfter(command, "--mcp-config ")[1])[0], "'")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			MCPServers map[string]struct {
				Args []string `json:"args"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(content, &config); err != nil {
			t.Fatal(err)
		}
		server, ok := config.MCPServers[ServerName]
		if len(config.MCPServers) != 1 || !ok || !slices.Equal(server.Args, c.want) {
			t.Fatalf("profile %q config = %s, want one %s server with args %v", c.profile, content, ServerName, c.want)
		}

		codex, err := ApplyProfile("codex", "/opt/bin/gate-inbox", dir, "codex", map[string]string{hooks.EnvSessionID: "abcd1234"}, "", c.profile)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(c.want)
		if !strings.Contains(codex, `mcp_servers.gate-inbox.args=`+string(encoded)) {
			t.Fatalf("profile %q codex launch = %q, want args %s", c.profile, codex, encoded)
		}
	}
}
