package mcpreg

import (
	"strings"

	"github.com/usestring/gate-inbox/internal/tmux"
)

// builtinDelegation names each CLI's own subagent mechanism, the way that
// CLI's model sees it in its tool list.
//
// Every CLI the board runs has one, and a model offered two ways to delegate
// takes the one its own standing instructions describe. The MCP server's
// instructions did not win that: with only them, claude 2.1.283 (sonnet) and
// codex 0.157 each picked their own subagent tool to hand off an
// investigation in every trial, and with the launch steering below they
// picked create_session in every trial. So each registration puts the
// steering where that CLI reads its own instructions, naming its own tool.
var builtinDelegation = map[string]string{
	"claude":   "the built-in Agent tool (also called Task: general-purpose, Explore, Plan, fork and custom agents) or the Workflow tool",
	"codex":    "the built-in spawn_agent tool (with send_input, wait_agent and close_agent)",
	"opencode": "the built-in subagent tool (the general, explore and other subagents)",
}

// DelegationSteering is the standing instruction for style's CLI. It is
// ASCII on purpose: codex receives it as a TOML string inside a -c override,
// and the quoting there is Go's %q, which only matches TOML for ASCII.
func DelegationSteering(style string) string {
	builtin := builtinDelegation[style]
	return strings.Join([]string{
		"# Delegating work: use Gate Inbox sessions",
		"",
		"Hand any unit of real work (an investigation, implementation, review or multi-step search) to a Gate Inbox session with create_session instead of using " + builtin + ". A session is on the user's board, and its questions are relayed to you. Keep the built-in tool for a quick read-only lookup.",
		"",
		"Call list_sessions first and reuse an idle session. Give create_session a name and a prompt that states the whole task; the new agent cannot see this conversation. Track shared plans with the task tool. Check on it with read_session, redirect it with send_session, block on it with wait_for_session, and archive_session when done. One session per workstream.",
		"",
		"If the gate-inbox tools are missing, use " + builtin + " as usual.",
		"",
	}, "\n") + "\n" + childDialogSteering
}

// childDialogSteering is the standing rule that a parent owns its children's
// dialogs. It rides every CLI's steering after the delegation section, kept
// as its own constant so other rules land beside it rather than inside it.
// ASCII for the same reason as DelegationSteering.
const childDialogSteering = `# Your children's dialogs are yours

When a session you created stops on a dialog, Gate Inbox relays it to you. Answer what your brief or your user's standing decisions settle. Ask your user the rest with your own question tool, copying each question and its options word for word, then reply with answer_session and relay: true. Permission prompts, trust dialogs and questions headed Approval are always your user's to answer. Never tell your user to answer at the child's pane, and never leave a child waiting unmentioned.
`

// claudeSteeringFile is the file a managed claude session appends to its
// system prompt with --append-system-prompt-file.
const claudeSteeringFile = generatedPrefix + "claude-delegation.md"

// SteeringFlag is the `gate-inbox mcp` flag naming the CLI whose standing
// instructions the server carries in its own block, for a CLI with nowhere
// else to put them. Its value is a style ServerSteering knows.
const SteeringFlag = "--steering"

// ServerSteering is what the MCP server appends to its instructions for
// style's CLI, and false for a style whose launch carries its steering
// itself. OpenCode v2 loads no instruction file a launch can name, so its
// naming and delegation steering both ride the server.
func ServerSteering(style string) (string, bool) {
	if style != "opencode" {
		return "", false
	}
	return string(renameSteering()) + "\n" + launchSteering(style), true
}

// claudeAppendFlag is the flag the claude launch carries. Claude Code keeps
// only the last --append-system-prompt-file it is given, so a command that
// already names one -- the operator's own -- keeps it and goes without
// ours: overriding an operator's system prompt is not ours to do. An
// --append-system-prompt string is a separate flag and survives alongside.
const claudeAppendFlag = "--append-system-prompt-file"

func withClaudeSteering(command, path string) string {
	if strings.Contains(command, claudeAppendFlag) {
		return command
	}
	return command + " " + claudeAppendFlag + " " + tmux.ShellQuote(path)
}
