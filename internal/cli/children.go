package cli

import (
	"io"
	"time"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

const usageChildren = "children [--session <id>] [--json]"

func childrenSection() section {
	return section{
		title: "Your children",
		commands: []command{
			{name: "children", usage: usageChildren, about: "list the sessions you spawned and, for each one stopped on a dialog, how long it has waited and its questions word for word, ready to put to the user; never touches tmux, so it is cheap to run every turn", run: configCommand(runChildren)},
		},
	}
}

func runChildren(out io.Writer, args []string, sessionID, configDir string) error {
	set := cmdline.NewFlagSet(usageChildren)
	session := set.String("session", "", "whose children to list; defaults to this session")
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	if *session != "" {
		sessionID = *session
	}
	board, err := sessioncmd.ChildDialogs(configDir, convo.ClaudeHome(), sessionID, time.Now())
	if err != nil {
		return err
	}
	return cmdline.Emit(out, *asJSON, board, sessioncmd.FormatChildBoard(board))
}
