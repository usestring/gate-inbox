package cli

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func runStop(out io.Writer, sessions sessionCommands, args []string, sessionID string) error {
	set := cmdline.NewFlagSet(usageStop)
	ifManaged := set.Bool("if-managed", false, "report unmanaged callers without stopping them")
	dryRun := set.Bool("dry-run", false, "verify the calling session without ending it")
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	result, err := sessions.Stop(sessionID, *dryRun)
	if errors.Is(err, sessioncmd.ErrUnmanagedCaller) && *ifManaged {
		return cmdline.Emit(out, *asJSON, sessioncmd.StopResult{DryRun: *dryRun}, "caller is unmanaged")
	}
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
	set := cmdline.NewFlagSet("_finish-stop <launch-time> <pane-id> <process-pin>")
	operands, err := parseCommand(os.Stdout, set, args, 3, 3)
	if err != nil {
		return err
	}
	// This runs as a run-shell -b job, which tmux SIGTERMs when ending the
	// last session empties the server and leaves its output pipe closed.
	signal.Ignore(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	_, err = sessioncmd.NewSessions(configDir, sessioncmd.CLIVocabulary()).FinishStop(sessionID, operands[0], operands[1], operands[2])
	return err
}
