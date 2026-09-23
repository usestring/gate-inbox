package cli

import (
	"io"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

const usagePriority = `priority <urgent|high|medium|low|none>`

func prioritySection() section {
	return section{
		title: "How much this session matters",
		commands: []command{
			{name: "priority", usage: usagePriority, about: "declare the tier of the work in this session, so triage hands it over ahead of the sessions that matter less", run: configCommand(runPriority)},
		},
	}
}

func runPriority(out io.Writer, args []string, sessionID, configDir string) error {
	set := newFlagSet(usagePriority)
	operands, err := parseCommand(out, set, args, 1, 1)
	if err != nil {
		return err
	}
	message, err := sessioncmd.Priority(configDir, sessionID, operands[0])
	return printMessage(out, message, err)
}
