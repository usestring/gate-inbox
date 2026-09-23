// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/mcpreg"
)

// Vocabulary is how one front spells the actions these errors send a caller
// after. Both fronts drive this layer, but an agent holds only one of them:
// a session whose CLI carries no MCP client has the subcommands and nothing
// else, so naming a tool at it points at something it cannot call.
type Vocabulary struct {
	ListSessions   string
	ListTerminals  string
	ListTasks      string
	ListGroups     string
	CreateGroup    string
	CreateTerminal string
	Revive         string
	Restore        string
	Send           string
	Read           string
	Answer         string
}

// MCPVocabulary spells the actions as the tools an MCP client sees.
func MCPVocabulary() Vocabulary {
	return Vocabulary{
		ListSessions:   "list_sessions",
		ListTerminals:  "list_terminals",
		ListTasks:      `task with action "list"`,
		ListGroups:     "list_groups",
		CreateGroup:    "create_group",
		CreateTerminal: "create_terminal",
		Revive:         "revive_session",
		Restore:        "archive_session archived false",
		Send:           "send_session",
		Read:           "read_session",
		Answer:         "answer_session",
	}
}

// cliCommand is how a session reaches the manager: through the path its
// launch put in the environment, since the manager runs from its checkout
// rather than from anyone's PATH. The bare name is the fallback for a shell
// that never got the variable -- it is what an adopted pane would type, and
// a wrong name in an error message beats an empty one.
const cliCommand = `"${GATE_INBOX_BIN:-gate-inbox}"`

// VocabularyFor spells the actions the way a session on this tool reaches
// them: as MCP tools when the manager registers its server into that CLI,
// as shell subcommands otherwise.
func VocabularyFor(toolName string, tool config.Tool) Vocabulary {
	if mcpreg.Style(toolName, tool.MCP) != mcpreg.StyleNone {
		return MCPVocabulary()
	}
	return CLIVocabulary()
}

// CLIVocabulary spells them as the subcommands an agent runs from its shell.
func CLIVocabulary() Vocabulary {
	return Vocabulary{
		ListSessions:   cliCommand + " sessions",
		ListTerminals:  cliCommand + " terminal list",
		ListTasks:      cliCommand + " task list",
		ListGroups:     cliCommand + " groups",
		CreateGroup:    cliCommand + " create-group",
		CreateTerminal: cliCommand + " terminal create",
		Revive:         cliCommand + " revive",
		Restore:        cliCommand + " archive --restore",
		Send:           cliCommand + " send",
		Read:           cliCommand + " read",
		Answer:         cliCommand + " answer",
	}
}
