package cli

import (
	"io"
	"os"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func runStop(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageStop)
	dryRun := set.Bool("dry-run", false, "verify the calling session without ending it")
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	result, err := sessions.Stop(sessionID, *dryRun)
	if err != nil {
		return err
	}
	message := "queued stop for " + sessioncmd.FormatSession(result.Target)
	if *dryRun {
		message = "would stop " + sessioncmd.FormatSession(result.Target)
	}
	return cmdline.Emit(out, *asJSON, result, message)
}

func finishStop(args []string, sessionID, configDir string) error {
	set := cmdline.NewFlagSet("_finish-stop <launch-time> <pane-id>")
	operands, err := parseCommand(os.Stdout, set, args, 2, 2)
	if err != nil {
		return err
	}
	_, err = sessioncmd.NewSessions(configDir, sessioncmd.CLIVocabulary()).FinishStop(sessionID, operands[0], operands[1])
	return err
}
