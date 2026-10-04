package mcpreg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// What every CLI's steering has to say: which tool to use instead of its
// own, what counts as work, the narrow carve-out, and why.
func assertSteers(t *testing.T, cli, steering, builtin string) {
	t.Helper()
	for _, want := range []string{
		"create_session instead of using " + builtin,
		"any unit of real work",
		"quick read-only lookup",
		"user's board",
		"its questions are relayed to you",
		"list_sessions",
		"wait_for_session",
	} {
		if !strings.Contains(steering, want) {
			t.Errorf("%s steering is missing %q:\n%s", cli, want, steering)
		}
	}
}

func TestDelegationSteeringNamesEachCLIsOwnSubagentTool(t *testing.T) {
	for style, want := range map[string]string{
		"claude":   "Agent tool",
		"codex":    "spawn_agent",
		"opencode": "task tool",
	} {
		steering := DelegationSteering(style)
		assertSteers(t, style, steering, builtinDelegation[style])
		if !strings.Contains(steering, want) {
			t.Errorf("%s steering never names %q", style, want)
		}
		for i, r := range steering {
			if r > 0x7e || (r < 0x20 && r != '\n') {
				t.Fatalf("%s steering has %q at %d; codex's -c override quotes it with %%q, which is TOML only for ASCII", style, r, i)
			}
		}
	}
}

func TestApplyClaudeAppendsTheDelegationSteering(t *testing.T) {
	dir := t.TempDir()
	command, err := Apply("claude", "/opt/bin/gate-inbox", dir, "claude", map[string]string{}, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, hooks.GeneratedName(claudeSteeringFile, []byte(launchSteering("claude"))))
	if want := " --append-system-prompt-file " + tmux.ShellQuote(path); !strings.HasSuffix(command, want) {
		t.Fatalf("command = %q, want it to end with %q", command, want)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertSteers(t, "claude", string(content), builtinDelegation["claude"])
}

// Claude Code keeps only the last --append-system-prompt-file, so ours
// would silently replace the operator's.
func TestApplyClaudeLeavesTheOperatorsAppendedPromptFileAlone(t *testing.T) {
	base := "claude --append-system-prompt-file /home/op/prompt.md"
	command, err := Apply("claude", "/opt/bin/gate-inbox", t.TempDir(), base, map[string]string{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(command, "--append-system-prompt-file") != 1 || !strings.Contains(command, "/home/op/prompt.md") {
		t.Fatalf("command = %q, want only the operator's prompt file", command)
	}
}

func TestPreviewClaudeWritesNoSteeringFile(t *testing.T) {
	dir := t.TempDir()
	command, err := Preview("claude", "/opt/bin/gate-inbox", dir, "claude", map[string]string{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, filepath.Join(dir, hooks.GeneratedName(claudeSteeringFile, []byte(launchSteering("claude"))))) {
		t.Fatalf("preview command = %q, want the steering path a launch would carry", command)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("a preview wrote %v (err %v)", entries, err)
	}
}

// Codex reads the override as TOML, so the test reads it the same way:
// unquoted from the command line and parsed back, it has to be the steering
// word for word.
func TestApplyCodexCarriesTheSteeringAsDeveloperInstructions(t *testing.T) {
	command, err := Apply("codex", "/opt/bin/gate-inbox", t.TempDir(), "codex", map[string]string{}, "")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.LastIndex(command, " -c ")
	if i < 0 {
		t.Fatalf("command = %q carries no -c override", command)
	}
	quoted := command[i+len(" -c "):]
	override := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(quoted, "'"), "'"), `'\''`, "'")
	if !strings.HasPrefix(override, "developer_instructions=") {
		t.Fatalf("last override = %q, want developer_instructions", override)
	}
	var parsed struct {
		DeveloperInstructions string `toml:"developer_instructions"`
	}
	if _, err := toml.Decode(override, &parsed); err != nil {
		t.Fatalf("override %q is not TOML: %v", override, err)
	}
	if parsed.DeveloperInstructions != launchSteering("codex") {
		t.Fatalf("developer_instructions = %q, want the codex steering", parsed.DeveloperInstructions)
	}
}

// OpenCode v2 loads no instruction file a launch can name, so its naming
// and delegation steering ride the MCP server's own block. No other CLI's
// server carries it: theirs rides the launch, where it outranks that block.
func TestServerSteeringCarriesOpencodeNamingAndDelegation(t *testing.T) {
	steering, ok := ServerSteering("opencode")
	if !ok {
		t.Fatal("opencode has no server steering")
	}
	assertSteers(t, "opencode", steering, builtinDelegation["opencode"])
	for _, want := range []string{"# Session naming", "not as your first action", `"rename"`} {
		if !strings.Contains(steering, want) {
			t.Fatalf("server steering is missing %q:\n%s", want, steering)
		}
	}
	for _, style := range []string{"", "claude", "codex", "none"} {
		if text, ok := ServerSteering(style); ok || text != "" {
			t.Fatalf("ServerSteering(%q) = %q, %v; want nothing", style, text, ok)
		}
	}
}

// Every CLI Gate Inbox launches carries the rule that a parent owns its
// children's dialogs, wherever that CLI reads its standing instructions.
func TestEveryCLISteeringCarriesTheChildDialogRule(t *testing.T) {
	for style := range builtinDelegation {
		text := DelegationSteering(style)
		for _, want := range []string{"Your children's dialogs are yours", "word for word", "answer_session",
			"relay: true", "Never tell your user to answer at the child's pane"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s steering lacks %q", style, want)
			}
		}
	}
	if text, _ := ServerSteering("opencode"); !strings.Contains(text, childDialogSteering) {
		t.Error("OpenCode's server-carried steering lacks the child-dialog rule")
	}
}

// An adopted opencode has no gate-inbox tools, so its steering names the CLI
// its shell can run as the session, and no MCP tool it does not have.
func TestAdoptedPluginSteeringNamesOnlyTheCLI(t *testing.T) {
	text := AdoptedPluginSteering("opencode")
	for _, want := range []string{`"$GATE_INBOX_BIN" spawn`, `"$GATE_INBOX_BIN" sessions`, "--relay", builtinDelegation["opencode"], "CROSS-SESSION-MESSAGE", "rename"} {
		if !strings.Contains(text, want) {
			t.Errorf("steering lacks %q", want)
		}
	}
	for _, tool := range []string{"create_session", "send_session", "list_sessions", "answer_session"} {
		if strings.Contains(text, tool) {
			t.Errorf("steering names the MCP tool %s", tool)
		}
	}
}
