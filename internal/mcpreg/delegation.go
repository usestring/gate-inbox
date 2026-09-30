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

// delegationSteering is the standing instruction for style's CLI. It is
// ASCII on purpose: codex receives it as a TOML string inside a -c override,
// and the quoting there is Go's %q, which only matches TOML for ASCII.
func delegationSteering(style string) string {
	builtin := builtinDelegation[style]
	return strings.Join([]string{
		"# Delegating work: use Gate Inbox sessions",
		"",
		"This session runs under Gate Inbox, which gives you the gate-inbox MCP tools: create_session, list_sessions, read_session, send_session, wait_for_session, archive_session and task. When you hand work to another agent, start a Gate Inbox session with create_session instead of using " + builtin + ". That covers any unit of real work: an investigation, an implementation, a review, a multi-step search, or anything else the user would want to see on their board.",
		"",
		"Why: a Gate Inbox session is on the user's board, where they can watch it, answer it and take it over. It works in a full context of its own instead of spending yours, it survives this conversation ending or being compacted, and its questions are relayed to you. Work done by a built-in subagent is invisible to the user and ends with this conversation.",
		"",
		"The built-in tool is still right for a quick read-only lookup that returns a single conclusion within seconds, such as finding where a symbol is defined. When in doubt, use a session.",
		"",
		"How: call list_sessions first and reuse a relevant idle session. Give create_session a descriptive name and a prompt that states the whole task, since the new agent cannot see this conversation; for repository work beside other agents, give it a directory that is its own checkout. Track shared plans with the task tool. Use read_session to check on it, send_session to redirect it, wait_for_session when your next step needs its result, and archive_session once it is done. Sessions cost the user tokens: create one per workstream, not one per trivial step.",
		"",
		"If the gate-inbox tools are not available in this session, delegate with " + builtin + " as usual.",
		"",
	}, "\n")
}

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
	return string(renameSteering()) + "\n" + delegationSteering(style), true
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
