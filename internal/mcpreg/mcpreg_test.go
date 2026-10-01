// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package mcpreg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
)

func TestStyleResolution(t *testing.T) {
	cases := []struct {
		tool, explicit, want string
	}{
		{"claude", "", "claude"},
		{"codex", "", "codex"},
		{"opencode", "", "opencode"},
		{"grok", "", "none"},
		{"gemini", "", "none"},
		{"hermes", "", "none"},
		{"aider", "", "none"},
		{"my-claude", "claude", "claude"},
		{"claude", "none", "none"},
		{"claude", "bogus", "none"},
	}
	for _, c := range cases {
		if got := Style(c.tool, c.explicit); got != c.want {
			t.Fatalf("Style(%q, %q) = %q, want %q", c.tool, c.explicit, got, c.want)
		}
	}
}

func TestApplyClaudeWritesConfigAndFlag(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{}
	command, err := Apply("claude", "/opt/bin/gate-inbox", dir, "claude", env, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, hooks.GeneratedName("gate-inbox-mcp-claude.json", claudeConfig("/opt/bin/gate-inbox")))
	if !strings.Contains(command, "--mcp-config") || !strings.Contains(command, path) {
		t.Fatalf("command = %q", command)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	server := parsed["mcpServers"]["gate-inbox"]
	if server.Command != "/opt/bin/gate-inbox" || len(server.Args) != 1 || server.Args[0] != "mcp" {
		t.Fatalf("server = %+v", server)
	}
	if server.Env["GATE_INBOX_SESSION_ID"] != "${GATE_INBOX_SESSION_ID}" || len(server.Env) != 1 {
		t.Fatalf("env = %v", server.Env)
	}
	if len(env) != 0 {
		t.Fatalf("claude style should not touch env, got %v", env)
	}
}

func TestApplyCodexAppendsOverrides(t *testing.T) {
	env := map[string]string{}
	command, err := Apply("codex", "/opt/bin/gate-inbox", t.TempDir(), "codex", env, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`mcp_servers.gate-inbox.command="/opt/bin/gate-inbox"`,
		`mcp_servers.gate-inbox.args=["mcp"]`,
		`mcp_servers.gate-inbox.env_vars=["GATE_INBOX_SESSION_ID"]`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("command %q missing %q", command, want)
		}
	}
}

func TestApplyOpencodeSetsEnvToConfigFile(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{}
	if _, err := Apply("opencode", "/opt/bin/gate-inbox", dir, "opencode", env, ""); err != nil {
		t.Fatal(err)
	}
	path := env["OPENCODE_CONFIG"]
	if path == "" {
		t.Fatal("OPENCODE_CONFIG not set")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{`"/opt/bin/gate-inbox"`, `"mcp"`, "{env:GATE_INBOX_SESSION_ID}"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config %q missing %q", text, want)
		}
	}
}

// v2 serves every session from one background service per user, which reads
// its own environment rather than the launching client's: the generated
// config never reaches the agent and the MCP server it spawns carries the id
// of whichever session started the service. A private server per session is
// what makes both land, so the v2 launch carries --standalone.
func TestApplyOpencodeV2RunsAPrivateServer(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{hooks.EnvSessionID: "abcd1234"}
	command, err := Apply("opencode", "/opt/bin/gate-inbox", dir, "opencode --prompt 'go'", env, "")
	if err != nil {
		t.Fatal(err)
	}
	if command != "opencode --prompt 'go' --standalone" {
		t.Fatalf("v2 launch = %q, want --standalone appended", command)
	}
	if want := filepath.Join(dir, hooks.GeneratedName("gate-inbox-mcp-opencode.json", opencodeConfig("/opt/bin/gate-inbox", ""))); env["OPENCODE_CONFIG"] != want {
		t.Fatalf("OPENCODE_CONFIG = %q, want the shared %q when no model is chosen", env["OPENCODE_CONFIG"], want)
	}
	content, err := os.ReadFile(env["OPENCODE_CONFIG"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), `"model"`) {
		t.Fatalf("a launch on the CLI's default must not pin a model:\n%s", content)
	}
}

// v2's TUI has no model flag, so a chosen model rides the generated config,
// and a session on one gets a config of its own rather than rewriting the
// shared file under every other session's feet.
func TestApplyOpencodeV2PutsTheModelInASessionConfig(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{hooks.EnvSessionID: "abcd1234"}
	if _, err := Apply("opencode", "/opt/bin/gate-inbox", dir, "opencode", env, "anthropic/claude-sonnet-5"); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, hooks.GeneratedName("gate-inbox-mcp-opencode-abcd1234.json", opencodeConfig("/opt/bin/gate-inbox", "anthropic/claude-sonnet-5"))); env["OPENCODE_CONFIG"] != want {
		t.Fatalf("OPENCODE_CONFIG = %q, want %q", env["OPENCODE_CONFIG"], want)
	}
	content, err := os.ReadFile(env["OPENCODE_CONFIG"])
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Model string `json:"model"`
		MCP   struct {
			Servers map[string]any `json:"servers"`
		} `json:"mcp"`
		Commands map[string]any `json:"commands"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Model != "anthropic/claude-sonnet-5" {
		t.Fatalf("model = %q, want the chosen one", parsed.Model)
	}
	if parsed.MCP.Servers["gate-inbox"] == nil || parsed.Commands["rename"] == nil {
		t.Fatalf("the session config must carry everything the shared one does:\n%s", content)
	}
}

func TestPreviewOpencodeV2NamesTheSessionConfigWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{hooks.EnvSessionID: "abcd1234"}
	command, err := Preview("opencode", "/opt/bin/gate-inbox", dir, "opencode", env, "anthropic/claude-sonnet-5")
	if err != nil {
		t.Fatal(err)
	}
	if command != "opencode --standalone" {
		t.Fatalf("preview command = %q", command)
	}
	if want := filepath.Join(dir, hooks.GeneratedName("gate-inbox-mcp-opencode-abcd1234.json", opencodeConfig("/opt/bin/gate-inbox", "anthropic/claude-sonnet-5"))); env["OPENCODE_CONFIG"] != want {
		t.Fatalf("OPENCODE_CONFIG = %q, want %q", env["OPENCODE_CONFIG"], want)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("a preview wrote %v (err %v)", entries, err)
	}
}

// OpenCode v2 reads servers from mcp.servers and slash commands from
// commands: it drops a v1 command block, and a v1 mcp.<name> entry serves
// its tools only through code mode. It also loads nothing from a config
// instructions list, so the steering rides the server, which the command
// names with the steering flag.
func TestApplyOpencodeWritesTheV2Schema(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{}
	if _, err := Apply("opencode", "/opt/bin/gate-inbox", dir, "opencode", env, ""); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(env["OPENCODE_CONFIG"])
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		MCP struct {
			Servers map[string]struct {
				Type        string            `json:"type"`
				Command     []string          `json:"command"`
				Environment map[string]string `json:"environment"`
				Codemode    *bool             `json:"codemode"`
			} `json:"servers"`
		} `json:"mcp"`
		Commands map[string]struct {
			Description string `json:"description"`
			Template    string `json:"template"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, v1 := range []string{`"command": {`, `"instructions"`, `"enabled"`} {
		if strings.Contains(string(content), v1) {
			t.Fatalf("generated config carries the v1 key %s:\n%s", v1, content)
		}
	}
	server, ok := parsed.MCP.Servers["gate-inbox"]
	if !ok || server.Type != "local" {
		t.Fatalf("no local gate-inbox server under mcp.servers:\n%s", content)
	}
	if want := []string{"/opt/bin/gate-inbox", "mcp", SteeringFlag, "opencode"}; !slices.Equal(server.Command, want) {
		t.Fatalf("server command = %q, want %q", server.Command, want)
	}
	if server.Environment[hooks.EnvSessionID] != "{env:"+hooks.EnvSessionID+"}" {
		t.Fatalf("environment = %v, want the session id forwarded", server.Environment)
	}
	if server.Codemode == nil || *server.Codemode {
		t.Fatalf("codemode = %v, want false so the tools are called directly", server.Codemode)
	}
	rename, ok := parsed.Commands["rename"]
	if !ok || rename.Template == "" {
		t.Fatalf("generated config registers no rename command: %s", content)
	}
	for _, want := range []string{`"rename"`, "gate-inbox", "$ARGUMENTS", "$GATE_INBOX_BIN"} {
		if !strings.Contains(rename.Template, want) {
			t.Fatalf("rename template is missing %q:\n%s", want, rename.Template)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("an opencode launch wrote %v (err %v), want the config alone", entries, err)
	}
}

// A dry run shows the same paths a launch would carry but writes nothing:
// the config lives in shared state a rehearsal must not touch.
func TestPreviewOpencodeWritesNothing(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{}
	if _, err := Preview("opencode", "/opt/bin/gate-inbox", dir, "opencode", env, ""); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, hooks.GeneratedName("gate-inbox-mcp-opencode.json", opencodeConfig("/opt/bin/gate-inbox", ""))); env["OPENCODE_CONFIG"] != want {
		t.Fatalf("OPENCODE_CONFIG = %q, want %q", env["OPENCODE_CONFIG"], want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a preview wrote %d files", len(entries))
	}
}

func TestApplyNoneLeavesEverything(t *testing.T) {
	env := map[string]string{}
	command, err := Apply("none", "/opt/bin/gate-inbox", t.TempDir(), "aider", env, "")
	if err != nil || command != "aider" || len(env) != 0 {
		t.Fatalf("command = %q, env = %v, err = %v", command, env, err)
	}
}
