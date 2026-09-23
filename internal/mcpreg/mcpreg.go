// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package mcpreg registers the Gate Inbox MCP server into the sessions
// the manager spawns, per tool. Each supported tool has a registration
// style: a launch flag or a generated config file. Generated artifacts live under the manager's hooks
// directory and reference the running binary, so any install location
// (homebrew, go install) works untouched.
package mcpreg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// ServerName is the MCP server entry every session gets, which names its
// tools mcp__gate-inbox__*.
const ServerName = "gate-inbox"

const serverName = ServerName

// ToolPrefix is how Claude Code names this server's tools in a permission
// rule or an --allowedTools list: the prefix before the tool's own name.
const ToolPrefix = "mcp__" + ServerName + "__"

// generatedPrefix names every file this package writes under the hooks
// directory. The directory can be shared with a build from before the
// rename, which writes the same files under the bare names with its own
// server name inside; a session reads its config at startup, so one shared
// path would hand it whichever build wrote last.
const generatedPrefix = "gate-inbox-"

const StyleNone = "none"

var knownStyles = map[string]bool{
	"claude":   true,
	"codex":    true,
	"opencode": true,
	StyleNone:  true,
}

// Style resolves a tool's registration style: the explicit `mcp` config
// value wins, otherwise a tool whose config key names a known style uses
// it, and anything else registers nothing.
func Style(toolName, explicit string) string {
	if explicit != "" {
		if knownStyles[explicit] {
			return explicit
		}
		return StyleNone
	}
	if knownStyles[toolName] {
		return toolName
	}
	return StyleNone
}

// Apply mutates a session launch so the tool sees the MCP server: it may
// append flags to command, add environment variables, write a config file
// under hooksDir, or run a one-time registration. exe is the Gate Inbox
// binary the generated configs point at. model is the model the session was
// asked to run on, for the tool whose generated config is the only place it
// can be named; every other style leaves it to the command line.
func Apply(style, exe, hooksDir, command string, env map[string]string, model string) (string, error) {
	return apply(style, exe, hooksDir, command, env, model, true)
}

// Preview is Apply without the side effects: the same command and the same
// environment, composed against the paths the configs would be written to,
// with nothing written and no registration run.
//
// It exists to show a person the command a launch would run. A rehearsal that wrote a config file, or shelled out
// to register an MCP server, would be doing part of the thing it is there to
// avoid doing -- and it fails on a machine where those paths are not writable,
// which is exactly when somebody is reading a dry run.
func Preview(style, exe, hooksDir, command string, env map[string]string, model string) (string, error) {
	return apply(style, exe, hooksDir, command, env, model, false)
}

func apply(style, exe, hooksDir, command string, env map[string]string, model string, write bool) (string, error) {
	config := func(name string, content []byte) (string, error) {
		if !write {
			return filepath.Join(hooksDir, name), nil
		}
		return writeConfig(hooksDir, name, content)
	}
	switch style {
	case "claude":
		path, err := config(generatedPrefix+"mcp-claude.json", claudeConfig(exe))
		if err != nil {
			return "", err
		}
		return command + " --mcp-config " + tmux.ShellQuote(path), nil
	case "codex":
		overrides := []string{
			fmt.Sprintf(`mcp_servers.%s.command=%q`, serverName, exe),
			fmt.Sprintf(`mcp_servers.%s.args=["mcp"]`, serverName),
			fmt.Sprintf(`mcp_servers.%s.env_vars=[%q]`, serverName, hooks.EnvSessionID),
		}
		for _, override := range overrides {
			command += " -c " + tmux.ShellQuote(override)
		}
		return command, nil
	case "opencode":
		// The rename steering rides a file of its own rather than living
		// inside the JSON: instructions are markdown, and the generated
		// config references it by absolute path. Written only when the
		// launch is real -- a dry run must not touch the shared directory.
		steering, err := config(renameSteeringFile, renameSteering())
		if err != nil {
			return "", err
		}
		name, content := generatedPrefix+"mcp-opencode.json", opencodeConfig(exe, steering, "")
		if model != "" {
			// v2's TUI has no model flag; the config's model key is where a
			// chosen model goes, so a session on one gets a config of its own.
			name, content = generatedPrefix+"mcp-opencode-"+env[hooks.EnvSessionID]+".json", opencodeConfig(exe, steering, model)
		}
		path, err := config(name, content)
		if err != nil {
			return "", err
		}
		env["OPENCODE_CONFIG"] = path
		// v2 runs every session against one background service per user,
		// started by whichever opencode ran first and living on from there.
		// That service reads its own environment, not the client's: the
		// config this launch points at is ignored, and the MCP server it
		// spawns resolves {env:GATE_INBOX_SESSION_ID} to the id of the
		// session that happened to start it, so every session's rename lands
		// on that one row. A private server per session is what makes the
		// generated config, and this session's identity, reach the agent.
		command += " --standalone"
		return command, nil
	default:
		return command, nil
	}
}

// forwardedSessionID is the env block that hands a session's id to the MCP
// server it spawns, in the tool's own reference syntax.
func forwardedSessionID(open, close string) map[string]string {
	return map[string]string{hooks.EnvSessionID: open + hooks.EnvSessionID + close}
}

func claudeConfig(exe string) []byte {
	config := map[string]any{
		"mcpServers": map[string]any{
			serverName: map[string]any{
				"command": exe,
				"args":    []string{"mcp"},
				"env":     forwardedSessionID("${", "}"),
			},
		},
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	return data
}

// opencodeConfig is the generated config a managed opencode session runs
// with. model, when set, is the model the session was asked for; v2 reads
// it from here because its TUI has no flag for one.
func opencodeConfig(exe, steering, model string) []byte {
	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"mcp": map[string]any{
			serverName: map[string]any{
				"type":        "local",
				"command":     []string{exe, "mcp"},
				"enabled":     true,
				"environment": forwardedSessionID("{env:", "}"),
			},
		},
		// /rename for the r key: the template runs as a durable user prompt
		// in the current session and schedules normal model execution.
		// Config sources merge, so every other source's commands are kept.
		"command": map[string]any{
			"rename": map[string]any{
				"description": "Name this session after the work it is doing",
				"template":    renameCommandTemplate,
			},
		},
		// Silent naming steering, loaded into system context at session
		// start. It replaces the visible rename directive other tools carry
		// in their first prompt: nothing here lands in the user message the
		// session's own title is eventually written from. Config sources
		// merge, so the project's own instructions are kept alongside this.
		"instructions": []string{steering},
	}
	if model != "" {
		config["model"] = model
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	return data
}

// renameCommandTemplate is the /rename the manager registers into every
// opencode session it starts. It mirrors the shared rename command: the
// MCP tool first, the shell command only as fallback, and one reporting line
// and then stop -- the operator asked for a name, not for work.
const renameCommandTemplate = `Call the "rename" tool on the "gate-inbox" MCP server with the name, then report in one line what it actually did and stop. Do not resume the previous task in this turn; the operator asked for a name, not for work.

Use the shell only when that tool is not available in this session: run "$GATE_INBOX_BIN" rename "<name>" once, bare, with no pipe after it so the exit status is the command's own. $GATE_INBOX_BIN is the path this session was launched with, because the manager runs from its own checkout and is not on PATH. If it fails with "read-only file system", that is the Bash sandbox refusing the manager's state directory, not the manager: run the same command once more with the sandbox disabled and report that result.

If $ARGUMENTS is non-empty, that is the name — kebab-case it and use it verbatim, without second-guessing the operator's wording.

Otherwise take the name from this conversation: 2-4 words, kebab-case, describing the broad feature or theme of the whole session, not the subtask in front of you right now. Prefer what the session has been about across its turns to whatever was said in the last one.

Report the result, never your intention: an error from the tool, or a non-zero exit or any error on the command's output, is a rename that did not happen. Never invent a fallback: renaming anything else leaves the manager's own name behind.`

// renameSteeringFile is the instructions file the generated opencode config
// points at, written beside it under the manager's own home.
const renameSteeringFile = generatedPrefix + "opencode-rename-instructions.md"

// renameSteering is the silent half of opencode naming: guidance the model
// reads in system context from session start, instead of a directive
// prepended to the first user message. Naming late, once the task is
// understood, is what keeps the name specific instead of vague.
func renameSteering() []byte {
	return []byte(`# Session naming

This session is managed by Gate Inbox, which lists it under a short name. After you understand what the session is about — not as your first action — name it once by calling the "rename" tool on the "gate-inbox" MCP server with a short 2-4 word kebab-case name for the broad feature or theme of the whole session (not one subtask of a larger feature). If that tool is not available in this session, run "$GATE_INBOX_BIN" rename "<name>" once in the shell instead. Rename only this once, then carry on with what you were doing. Do not rename again later unless the user explicitly asks.
`)
}

// writeConfig writes content only when it changed, so concurrent spawns
// reading the same path never observe a partial rewrite of identical bytes.
func writeConfig(dir, name string, content []byte) (string, error) {
	path := filepath.Join(dir, name)
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(content) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
