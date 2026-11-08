package opencode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestGlobalConfigDirFollowsOpencode(t *testing.T) {
	t.Setenv("HOME", "/Users/someone")
	t.Setenv(ConfigDirEnv, "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, _ := GlobalConfigDir(); got != "/Users/someone/.config/opencode" {
		t.Errorf("default: %s", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, _ := GlobalConfigDir(); got != "/xdg/opencode" {
		t.Errorf("XDG_CONFIG_HOME: %s", got)
	}
	t.Setenv(ConfigDirEnv, "/explicit")
	if got, _ := GlobalConfigDir(); got != "/explicit" {
		t.Errorf("%s: %s", ConfigDirEnv, got)
	}
}

func testPlugin(home string) Plugin {
	return Plugin{Home: home, Bin: "/opt/gate-inbox/bin/gate-inbox", Tools: []string{"opencode"}, Steering: "steer \"quoted\" </script> "}
}

// Registering writes one file of the board's own beside the operator's
// plugins, writes nothing when it is already in place, and unregistering
// removes only Gate Inbox's files: this board's, or every board's.
func TestRegisterPluginTouchesOnlyItsOwnFile(t *testing.T) {
	dir := t.TempDir()
	plugins := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(plugins, "session-tracker.js")
	lookalike := filepath.Join(plugins, "gate-inbox-mine.js")
	for _, f := range []string{theirs, lookalike} {
		if err := os.WriteFile(f, []byte("// the operator's own\nexport default {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(config, []byte("{ // mine\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, b := testPlugin("/Users/u/.config/gate-inbox"), testPlugin("/srv/other board")
	for _, p := range []Plugin{a, b} {
		changed, err := RegisterPlugin(dir, p)
		if err != nil || !changed {
			t.Fatalf("register %s: changed=%v err=%v", p.Home, changed, err)
		}
	}
	if PluginPath(dir, a.Home) == PluginPath(dir, b.Home) {
		t.Fatal("two boards share one plugin file")
	}
	if changed, err := RegisterPlugin(dir, a); err != nil || changed {
		t.Fatalf("second register: changed=%v err=%v", changed, err)
	}
	if !PluginRegistered(dir, a) {
		t.Fatal("registered plugin not recognised")
	}
	moved := a
	moved.Bin = "/usr/local/bin/gate-inbox"
	if PluginRegistered(dir, moved) {
		t.Fatal("a plugin naming another binary reads as current")
	}
	if changed, _ := RegisterPlugin(dir, moved); !changed {
		t.Fatal("a moved binary did not rewrite the plugin")
	}

	if changed, err := UnregisterPlugin(dir, a.Home); err != nil || !changed {
		t.Fatalf("unregister a: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(PluginPath(dir, a.Home)); err == nil {
		t.Fatal("a's plugin is still there")
	}
	if _, err := os.Stat(PluginPath(dir, b.Home)); err != nil {
		t.Fatal("removing a's plugin removed b's")
	}
	if changed, err := UnregisterPlugin(dir, ""); err != nil || !changed {
		t.Fatalf("unregister all: changed=%v err=%v", changed, err)
	}
	entries, _ := os.ReadDir(plugins)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != "gate-inbox-mine.js,session-tracker.js" {
		t.Fatalf("plugins left: %v", left)
	}
	if raw, _ := os.ReadFile(config); string(raw) != "{ // mine\n}\n" {
		t.Fatalf("config touched: %q", raw)
	}
	if changed, err := UnregisterPlugin(t.TempDir(), a.Home); err != nil || changed {
		t.Fatalf("unregister from an empty dir: changed=%v err=%v", changed, err)
	}
}

func TestPluginSourceNamesItsBoard(t *testing.T) {
	p := testPlugin(`/Users/u/it's "here"`)
	src := string(p.Source())
	first, _, _ := strings.Cut(src, "\n")
	if !strings.HasPrefix(first, pluginTag) {
		t.Fatalf("first line %q", first)
	}
	path := filepath.Join(t.TempDir(), "p.js")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if owner, ok := pluginOwner(path); !ok || owner != p.Home {
		t.Fatalf("owner %q %v", owner, ok)
	}
	for _, placeholder := range []string{"__HOME__", "__BIN__", "__TOOLS__", "__STEERING__", "__KEY__"} {
		if strings.Contains(src, placeholder) {
			t.Errorf("%s left in the source", placeholder)
		}
	}
}

// pluginHarness loads a plugin file the way opencode's service does, with a
// stand-in for the context it hands a v2 plugin, and plays steps against the
// hooks it registered. Each step's result is one JSON line.
const pluginHarness = `
import { readFileSync, rmSync, writeFileSync } from "fs";
const [plugin, stepsFile] = process.argv.slice(2);
const steps = JSON.parse(readFileSync(stepsFile, "utf8"));
const hooks = {};
const reg = (domain) => ({ hook: async (name, fn) => { hooks[domain + "." + name] = fn; } });
const mod = await import(plugin);
await mod.default.setup({ tool: reg("tool"), shell: reg("shell"), session: reg("session") });
for (const step of steps) {
  if (step.op === "rm") { rmSync(step.path, { force: true }); console.log("{}"); continue; }
  if (step.op === "write") { writeFileSync(step.path, step.content); console.log("{}"); continue; }
  if (step.op === "shell") {
    await hooks["tool.execute.before"]({ tool: "shell", sessionID: step.session, input: { command: step.command } });
    const shell = { command: step.command, env: step.env };
    await hooks["shell.create.before"](shell);
    console.log(JSON.stringify({ env: shell.env }));
    continue;
  }
  if (step.op === "context") {
    const request = { sessionID: step.session, system: [] };
    await hooks["session.context"](request);
    console.log(JSON.stringify({ system: request.system }));
  }
}
`

type harnessStep struct {
	Op      string            `json:"op"`
	Path    string            `json:"path,omitempty"`
	Content string            `json:"content,omitempty"`
	Session string            `json:"session,omitempty"`
	Command string            `json:"command,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type harnessResult struct {
	Env    map[string]string `json:"env"`
	System []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"system"`
}

func runHarness(t *testing.T, node, plugin string, steps []harnessStep) []harnessResult {
	t.Helper()
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.mjs")
	stepsFile := filepath.Join(dir, "steps.json")
	raw, _ := json.Marshal(steps)
	if err := os.WriteFile(harness, []byte(pluginHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stepsFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, harness, plugin, stepsFile)
	cmd.Env = tmuxtest.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	var results []harnessResult
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var r harnessResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("harness line %q: %v\n%s", line, err, out)
		}
		results = append(results, r)
	}
	if len(results) != len(steps) {
		t.Fatalf("%d results for %d steps:\n%s", len(results), len(steps), out)
	}
	return results
}

// TestGlobalPluginBehaviour runs the generated plugin under node against a
// scratch board home: inert with no board, for a pane the board has not
// adopted, for another CLI's marker and for a launched session; once a
// session's shell shows it in an adopted opencode pane, its commands speak as
// the row, the row learns its conversation, and its requests carry the
// steering; a marker naming the conversation is enough after a reload; and a
// pane let go of stops it.
func TestGlobalPluginBehaviour(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	home := t.TempDir()
	adopted := filepath.Join(home, "hooks", "adopted")
	if err := os.MkdirAll(adopted, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Plugin{Home: home, Bin: "/bin/sh", Tools: []string{"opencode"}, Steering: "STEER-ME"}
	dir := t.TempDir()
	if _, err := RegisterPlugin(dir, p); err != nil {
		t.Fatal(err)
	}
	plugin := PluginPath(dir, home)
	live := strconv.Itoa(os.Getpid())
	lock := filepath.Join(home, "manager.lock")
	marker := filepath.Join(adopted, "4242%7")
	pane := map[string]string{"TMUX": "/run/tmux-test/default,4242,0", "TMUX_PANE": "%7", "PATH": "/bin"}
	other := map[string]string{"TMUX": "/run/tmux-test/default,4242,0", "TMUX_PANE": "%8"}
	launched := map[string]string{"TMUX": "/run/tmux-test/default,4242,0", "TMUX_PANE": "%7", "GATE_INBOX_SESSION_ID": "launched1"}
	mailbox := filepath.Join(home, "hooks", "row1.conversation")

	steps := []harnessStep{
		{Op: "write", Path: marker, Content: "row1 " + live + " opencode\n"},
		{Op: "shell", Session: "ses_a", Command: "ls", Env: pane},       // 1: no board
		{Op: "context", Session: "ses_a"},                               // 2
		{Op: "write", Path: lock, Content: live + "\n"},                 // 3
		{Op: "shell", Session: "ses_b", Command: "pwd", Env: other},     // 4: pane not adopted
		{Op: "shell", Session: "ses_c", Command: "id", Env: launched},   // 5: launched session
		{Op: "context", Session: "ses_b"},                               // 6
		{Op: "shell", Session: "ses_a", Command: "ls -la", Env: pane},   // 7: adopted
		{Op: "context", Session: "ses_a"},                               // 8
		{Op: "context", Session: "ses_b"},                               // 9
		{Op: "write", Path: marker, Content: "row1 " + live + "\n"},     // 10: a claude's marker
		{Op: "context", Session: "ses_a"},                               // 11
		{Op: "shell", Session: "ses_a", Command: "true", Env: pane},     // 12
		{Op: "write", Path: marker, Content: "row1 " + live + " codex"}, // 13: another CLI's
		{Op: "shell", Session: "ses_a", Command: "date", Env: pane},     // 14
	}
	results := runHarness(t, node, plugin, steps)
	if env := results[1].Env; env["GATE_INBOX_SESSION_ID"] != "" || len(results[2].System) != 0 {
		t.Errorf("no board: env %v, system %v", env, results[2].System)
	}
	if env := results[4].Env; env["GATE_INBOX_SESSION_ID"] != "" {
		t.Errorf("unadopted pane got %v", env)
	}
	if env := results[5].Env; env["GATE_INBOX_SESSION_ID"] != "launched1" || env["GATE_INBOX_BIN"] != "" {
		t.Errorf("launched session changed: %v", env)
	}
	if len(results[6].System) != 0 || len(results[9].System) != 0 {
		t.Errorf("an unbound session got steering: %v %v", results[6].System, results[9].System)
	}
	env := results[7].Env
	if env["GATE_INBOX_SESSION_ID"] != "row1" || env["GATE_INBOX_BIN"] != "/bin/sh" || env["GATE_INBOX_HOME"] != home || env["PATH"] != "/bin" {
		t.Errorf("adopted shell env: %v", env)
	}
	if got := results[8].System; len(got) != 1 || got[0].Type != "text" || got[0].Text != "STEER-ME" {
		t.Errorf("adopted steering: %v", got)
	}
	if raw, err := os.ReadFile(mailbox); err != nil || string(raw) != "ses_a\n" {
		t.Errorf("conversation mailbox: %q %v", raw, err)
	}
	if len(results[11].System) != 0 || results[12].Env["GATE_INBOX_SESSION_ID"] != "" || results[14].Env["GATE_INBOX_SESSION_ID"] != "" {
		t.Errorf("a non-opencode marker still speaks: %v %v %v", results[11].System, results[12].Env, results[14].Env)
	}

	// A fresh plugin, as after a reload: the marker naming the conversation
	// is enough, and the pane let go of, or the board gone, ends it.
	steps = []harnessStep{
		{Op: "write", Path: marker, Content: "row1 " + live + " opencode ses_a\n"},
		{Op: "context", Session: "ses_a"}, // 1
		{Op: "context", Session: "ses_z"}, // 2
		{Op: "rm", Path: marker},
		{Op: "context", Session: "ses_a"}, // 4
		{Op: "write", Path: marker, Content: "row1 " + live + " opencode ses_a\n"},
		{Op: "write", Path: lock, Content: "999999999\n"},
		{Op: "context", Session: "ses_a"},                         // 7
		{Op: "shell", Session: "ses_a", Command: "ls", Env: pane}, // 8
	}
	results = runHarness(t, node, plugin, steps)
	if len(results[1].System) != 1 || len(results[2].System) != 0 {
		t.Errorf("marked conversation: %v / unmarked: %v", results[1].System, results[2].System)
	}
	if len(results[4].System) != 0 {
		t.Errorf("released pane still steered: %v", results[4].System)
	}
	if len(results[7].System) != 0 || results[8].Env["GATE_INBOX_SESSION_ID"] != "" {
		t.Errorf("dead board still speaks: %v %v", results[7].System, results[8].Env)
	}
}
