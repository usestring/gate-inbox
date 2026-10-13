package mcprelay

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The relay's command with its binary gone still answers as an MCP server
// with no tools, using nothing but sh, so Claude Code never shows it failed.
func TestTheScriptAnswersWithNoBinary(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`,
		`{"method":"notifications/initialized","jsonrpc":"2.0"}`,
		`{"method":"tools/list","jsonrpc":"2.0","id":1}`,
		`{"method":"tools/call","params":{"name":"x"},"jsonrpc":"2.0","id":"two"}`,
		`{"method":"ping","jsonrpc":"2.0","id":3}`,
	}, "\n") + "\n"
	cmd := exec.Command("/bin/sh", "-c", Script(t.TempDir(), filepath.Join(t.TempDir(), "gone")))
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the script failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 4 {
		t.Fatalf("answers = %q, want one per request", lines)
	}
	want := []string{`"id":0,"result":{"protocolVersion"`, `"id":1,"result":{"tools":[]}`, `"id":"two","error"`, `"id":3,"result":{}`}
	for i, line := range lines {
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("answer %d is not JSON: %q", i, line)
		}
		if !strings.Contains(line, want[i]) {
			t.Fatalf("answer %d = %q, want %q in it", i, line, want[i])
		}
	}
}

// With the binary there, the script hands the session to its relay, with
// the board's home, and answers nothing itself.
func TestTheScriptExecsTheInstalledRelay(t *testing.T) {
	home, bin := t.TempDir(), filepath.Join(t.TempDir(), "gate inbox's")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$1 $GATE_INBOX_HOME\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", Script(home, bin))
	cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":0,"method":"initialize"}` + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "mcp-relay "+home {
		t.Fatalf("the script ran %q", got)
	}
}

// fakeClaude keeps the user-scope MCP entries of path the way `claude mcp
// add-json -s user` and `claude mcp remove -s user` do.
func fakeClaude(t *testing.T, path string, calls *[]string) Claude {
	return func(args ...string) error {
		*calls = append(*calls, strings.Join(args[:5], " "))
		state := map[string]any{}
		if raw, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(raw, &state); err != nil {
				t.Fatal(err)
			}
		}
		servers, _ := state["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		switch args[1] {
		case "add-json":
			var spec any
			if err := json.Unmarshal([]byte(args[5]), &spec); err != nil {
				t.Fatal(err)
			}
			servers[args[4]] = spec
		case "remove":
			delete(servers, args[4])
		}
		state["mcpServers"] = servers
		data, _ := json.Marshal(state)
		return os.WriteFile(path, data, 0o600)
	}
}

func TestRegisterAddsReplacesAndLeavesOthersAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	home, other := t.TempDir(), t.TempDir()
	var calls []string
	claude := fakeClaude(t, path, &calls)

	if changed, err := Register(claude, path, home, "/opt/gate-inbox"); err != nil || !changed {
		t.Fatalf("first register: %v, %v", changed, err)
	}
	if changed, err := Register(claude, path, home, "/opt/gate-inbox"); err != nil || changed {
		t.Fatalf("a second register changed something: %v, %v", changed, err)
	}
	if changed, err := Register(claude, path, home, "/usr/local/bin/gate-inbox"); err != nil || !changed {
		t.Fatalf("register on a moved binary: %v, %v", changed, err)
	}
	want := "mcp add-json -s user gate-inbox|mcp remove -s user gate-inbox|mcp add-json -s user gate-inbox"
	if got := strings.Join(calls, "|"); got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
	if state, _ := Lookup(path, home, "/usr/local/bin/gate-inbox"); state != Current {
		t.Fatalf("state after registering = %v", state)
	}
	// Another board whose home is still there keeps its relay.
	if state, _ := Lookup(path, other, "/opt/gate-inbox"); state != OtherBoard {
		t.Fatalf("another board sees %v", state)
	}
	if _, err := Register(claude, path, other, "/opt/gate-inbox"); err == nil {
		t.Fatal("a second board replaced the first one's relay")
	}
	if changed, _ := Unregister(claude, path, other, false); changed {
		t.Fatal("another board's uninstall removed this board's relay")
	}
	if changed, err := Unregister(claude, path, home, false); err != nil || !changed {
		t.Fatalf("unregister: %v, %v", changed, err)
	}
	if state, _ := Lookup(path, home, ""); state != Absent {
		t.Fatalf("state after unregistering = %v", state)
	}
}

func TestRegisterLeavesAnOperatorsOwnServerAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"gate-inbox":{"type":"stdio","command":"/opt/op/gate-inbox","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	claude := fakeClaude(t, path, &calls)
	if _, err := Register(claude, path, t.TempDir(), "/opt/gate-inbox"); err == nil {
		t.Fatal("registered over an operator's own server")
	}
	if changed, _ := Unregister(claude, path, t.TempDir(), true); changed || len(calls) != 0 {
		t.Fatalf("unregister touched an operator's own server: %v", calls)
	}
}

// Against the real claude CLI, in a scratch config: the registered relay with
// its binary gone is a connected server, not a failed one.
func TestTheRealClaudeSeesTheRelayConnectedWithNoBinary(t *testing.T) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not installed")
	}
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	path, err := ClaudeStatePath()
	if err != nil || path != filepath.Join(configDir, ".claude.json") {
		t.Fatalf("state path = %q, %v", path, err)
	}
	claude := func(args ...string) error {
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("claude %v: %v\n%s", args, err, out)
		}
		return nil
	}
	home := t.TempDir()
	if _, err := Register(claude, path, home, filepath.Join(t.TempDir(), "gone")); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "mcp", "list").CombinedOutput()
	if err != nil {
		t.Fatalf("claude mcp list: %v\n%s", err, out)
	}
	line := ""
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, ServerName+": ") {
			line = l
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(line), "Connected") {
		t.Fatalf("claude mcp list says:\n%s", out)
	}
	if changed, err := Unregister(claude, path, home, false); err != nil || !changed {
		t.Fatalf("unregister: %v, %v", changed, err)
	}
}
