// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package mcpreg

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	path := filepath.Join(dir, "gate-inbox-mcp-claude.json")
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
	if want := filepath.Join(dir, "gate-inbox-mcp-opencode.json"); env["OPENCODE_CONFIG"] != want {
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
	if want := filepath.Join(dir, "gate-inbox-mcp-opencode-abcd1234.json"); env["OPENCODE_CONFIG"] != want {
		t.Fatalf("OPENCODE_CONFIG = %q, want %q", env["OPENCODE_CONFIG"], want)
	}
	content, err := os.ReadFile(env["OPENCODE_CONFIG"])
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Model string         `json:"model"`
		MCP   map[string]any `json:"mcp"`
		Cmd   map[string]any `json:"command"`
		Instr []string       `json:"instructions"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Model != "anthropic/claude-sonnet-5" {
		t.Fatalf("model = %q, want the chosen one", parsed.Model)
	}
	if parsed.MCP["gate-inbox"] == nil || parsed.Cmd["rename"] == nil || len(parsed.Instr) != 1 {
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
	if want := filepath.Join(dir, "gate-inbox-mcp-opencode-abcd1234.json"); env["OPENCODE_CONFIG"] != want {
		t.Fatalf("OPENCODE_CONFIG = %q, want %q", env["OPENCODE_CONFIG"], want)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("a preview wrote %v (err %v)", entries, err)
	}
}

func TestApplyOpencodeRegistersRenameCommandAndSteering(t *testing.T) {
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
		Command map[string]struct {
			Description string `json:"description"`
			Template    string `json:"template"`
		} `json:"command"`
		Instructions []string `json:"instructions"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	rename, ok := parsed.Command["rename"]
	if !ok || rename.Template == "" {
		t.Fatalf("generated config registers no rename command: %s", content)
	}
	for _, want := range []string{`"rename"`, "gate-inbox", "$ARGUMENTS", "$GATE_INBOX_BIN"} {
		if !strings.Contains(rename.Template, want) {
			t.Fatalf("rename template is missing %q:\n%s", want, rename.Template)
		}
	}
	if len(parsed.Instructions) != 1 {
		t.Fatalf("instructions = %v, want the one generated steering file", parsed.Instructions)
	}
	steering, err := os.ReadFile(parsed.Instructions[0])
	if err != nil {
		t.Fatalf("steering file %q unreadable: %v", parsed.Instructions[0], err)
	}
	for _, want := range []string{"not as your first action", `"rename"`, "$GATE_INBOX_BIN"} {
		if !strings.Contains(string(steering), want) {
			t.Fatalf("steering is missing %q:\n%s", want, steering)
		}
	}
}

// A dry run shows the same paths a launch would carry but writes nothing:
// the steering file lives in shared state a rehearsal must not touch.
func TestPreviewOpencodeWritesNothing(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{}
	if _, err := Preview("opencode", "/opt/bin/gate-inbox", dir, "opencode", env, ""); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "gate-inbox-mcp-opencode.json"); env["OPENCODE_CONFIG"] != want {
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
