// Package migrate moves a conversation from one agent CLI to another. The
// CLIs share no transcript format and none can resume another's session, so
// the move is a fresh session on the target tool whose first prompt points
// at the source's transcript on disk and says: read it, then carry on. The
// TUI key, the MCP tool and the CLI subcommand all build the same prompt
// here, so an agent taking over reads the same brief whichever surface asked.
package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tooldrivers"
)

// Transcript is where a source conversation can be read from: a file on
// disk, or a command that prints it when the tool keeps it in a database.
type Transcript struct {
	// Path is the transcript file; empty when Command is set instead.
	Path string
	// Command prints the transcript when run from the session's directory.
	Command string
	// Format tells the taking-over agent how the transcript is laid out, so
	// it reads the conversation rather than guessing at a JSON shape.
	Format string
	// Kind names the layout in the manager's own vocabulary ("claude",
	// "codex"), which is what the handover filter switches on. Empty for a
	// transcript the manager cannot filter.
	Kind string
}

// Roots are the agent CLIs' state directories the locators read.
type Roots struct {
	ClaudeHome string
	CodexRoot  string
}

// DefaultRoots resolves the roots the way id capture and history search do.
func DefaultRoots() Roots {
	roots := Roots{ClaudeHome: strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))}
	if home := os.Getenv("CODEX_HOME"); home != "" {
		roots.CodexRoot = filepath.Join(home, "sessions")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if roots.ClaudeHome == "" {
			roots.ClaudeHome = filepath.Join(home, ".claude")
		}
		if roots.CodexRoot == "" {
			roots.CodexRoot = filepath.Join(home, ".codex", "sessions")
		}
	}
	return roots
}

// Format picks the transcript format a tool's rows are read as: the
// session_store names it for tools that mint their own ids, and a tool named
// for a format the manager knows ("claude") is that format whatever wrapper
// its command runs. Empty when the tool keeps nothing the manager can find.
func Format(toolName string, tool config.Tool) string {
	if tool.SessionStore != "" {
		return tool.SessionStore
	}
	switch toolName {
	case search.ToolClaude, search.ToolCodex, search.ToolOpenCode:
		return toolName
	}
	return ""
}

// Locate finds the source session's transcript. The session must have a
// captured conversation id, and its tool a format the manager knows how to
// find on disk; a session that never took a turn has no transcript yet.
func Locate(roots Roots, toolName string, tool config.Tool, source store.Session) (Transcript, error) {
	if tool.Shell {
		return Transcript{}, fmt.Errorf("%s is a shell, not an agent - there is no conversation to migrate", source.Name)
	}
	if source.AgentSessionID == "" {
		return Transcript{}, fmt.Errorf("%s has no captured conversation id", source.Name)
	}
	format := Format(toolName, tool)
	switch format {
	case search.ToolClaude, search.ToolCodex:
		locator := search.NewLocator(roots.ClaudeHome, roots.CodexRoot)
		target, ok := locator.Target(source.ID, format, source.Cwd, source.AgentSessionID)
		if !ok || target.Path == "" {
			return Transcript{}, fmt.Errorf("no %s transcript on disk for %s (conversation %s); a session that has not taken a turn has none yet", format, source.Name, source.AgentSessionID)
		}
		return Transcript{Path: target.Path, Format: formatNotes[format], Kind: format}, nil
	case search.ToolOpenCode:
		return Transcript{Command: opencodeExportCommand(source.AgentSessionID), Format: formatNotes[format], Kind: format}, nil
	case "":
	default:
		if transcript, ok, err := locateByDriver(format, source); ok || err != nil {
			return transcript, err
		}
	}
	return Transcript{}, fmt.Errorf("tool %s keeps its conversations somewhere Gate Inbox cannot locate; claude, codex and opencode sessions can be migrated, and any tool whose session_store names an extension driver that supports it", toolName)
}

// locateByDriver asks the extension driver named by the tool's
// session_store. ok is false when no driver answers the format, or the
// driver cannot hand a conversation over, so the caller's refusal stands.
// The transcript has no Kind: the handover filter reads only the layouts the
// core knows.
func locateByDriver(format string, source store.Session) (Transcript, bool, error) {
	driver, ok, err := tooldrivers.Lookup(format)
	if err != nil || !ok {
		return Transcript{}, false, err
	}
	ctx, cancel := tooldrivers.Context()
	defer cancel()
	found, err := driver.MigrateTranscript(ctx, extension.TranscriptRequest{ID: source.AgentSessionID, Directory: source.Cwd})
	if errors.Is(err, errors.ErrUnsupported) {
		return Transcript{}, false, nil
	}
	if err != nil {
		return Transcript{}, true, fmt.Errorf("locating %s's %s transcript: %w", source.Name, format, err)
	}
	if (found.Path == "") == (found.Command == "") {
		return Transcript{}, true, fmt.Errorf("the %s driver must locate %s's transcript by exactly one of a path and a command", format, source.Name)
	}
	return Transcript{Path: found.Path, Command: found.Command, Format: found.Format}, true, nil
}

// opencodeExportCommand prints a session's transcript.
func opencodeExportCommand(id string) string {
	return "opencode session export " + shellWord(id)
}

// formatNotes is what the taking-over agent is told about each layout, so
// it reads the conversation out of the file rather than every tool result.
var formatNotes = map[string]string{
	search.ToolClaude:   `Claude Code's transcript: one JSON object per line. Records with "type":"user" and "type":"assistant" carry the conversation in message.content (text blocks, tool_use and tool_result blocks); skip records with "isMeta":true, and treat a "type":"summary" record as the compaction summary of everything before it.`,
	search.ToolCodex:    `Codex CLI's rollout: one JSON object per line. The first record is session_meta; "response_item" records carry the conversation (payload.type is message, function_call or function_call_output) and "event_msg" records carry user and agent messages.`,
	search.ToolOpenCode: `OpenCode's export: one JSON document opening with the session's info block, followed by every message with its role and parts.`,
}

// Brief is what the prompt is built from.
type Brief struct {
	Source     store.Session
	SourceTool string
	Transcript Transcript
	// SourceRunning says the previous agent is still up on the board, so
	// the new one is told it can ask it directly.
	SourceRunning bool
	// Words spells the manager's read and send actions the way the target's
	// surface names them.
	ReadAction string
	SendAction string
	// FilterNote is what the handover filter removed, written for the
	// taking-over agent. Empty when the transcript is handed over as it
	// stands.
	FilterNote string
}

// Prompt is the first prompt of the taking-over session. It rides the
// command line, so it carries no manager band: it is the task the spawn
// asked for, not a note typed in unasked.
func Prompt(brief Brief) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are taking over a conversation that until now ran on %s, as Gate Inbox session %q (id %s), in this same working directory: %s.\n\n",
		brief.SourceTool, brief.Source.Name, brief.Source.ID, brief.Source.Cwd)
	if brief.Transcript.Path != "" {
		if brief.FilterNote != "" {
			fmt.Fprintf(&b, "Its filtered transcript is the file %s\n", brief.Transcript.Path)
		} else {
			fmt.Fprintf(&b, "Its full transcript is the file %s\n", brief.Transcript.Path)
		}
	} else {
		fmt.Fprintf(&b, "Its full transcript prints from this command, run here: %s\n", brief.Transcript.Command)
	}
	if brief.Transcript.Format != "" {
		b.WriteString(brief.Transcript.Format)
		b.WriteString("\n")
	}
	if brief.FilterNote != "" {
		b.WriteString("\n" + brief.FilterNote + "\n")
	}
	b.WriteString("\nBefore doing anything else, read that transcript: start from its end to find what the user last asked for and what was in progress, then read back as far as you need to know the goal, what has been established, what was tried and rejected, and any constraints the user set. It can be large, so read it in slices rather than all at once.\n\n")
	b.WriteString("Then continue the work exactly where it left off, as if you were the same agent: keep the same goal, decisions and constraints, do not redo steps the transcript shows finished, and do not ask the user anything the transcript already answers. Open with one short line saying you have taken over and what you are doing next, then get on with it.")
	if brief.SourceRunning && brief.SendAction != "" && brief.ReadAction != "" {
		fmt.Fprintf(&b, "\n\nThe previous agent is still running as Gate Inbox session %s. If the transcript leaves something you need unclear, ask it with %s and read its answer with %s rather than guessing.",
			brief.Source.ID, brief.SendAction, brief.ReadAction)
	}
	return b.String()
}

// NewSession is the row the move creates: the target tool in the source's
// group and directory. The name is the operator's, so no rename directive
// follows it.
func NewSession(id, name, toolName string, source store.Session, plan launch.Plan) store.Session {
	return store.Session{
		ID:               id,
		MigrationID:      source.ID,
		MigrationOpening: source.MigrationOpening,
		Name:             name,
		Tool:             toolName,
		Cwd:              source.Cwd,
		Group:            source.Group,
		Status:           status.Starting,
		AgentSessionID:   plan.AgentSessionID,
		PendingInputs:    plan.PendingInputs,
		LaunchPrompt:     plan.LaunchPrompt,
		Model:            plan.Model,
		Account:          plan.Account,
		NameSource:       store.SourceUser,
	}
}

// shellWord quotes a value for the command a human or agent will paste.
func shellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
