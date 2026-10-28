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
	archive := set.Bool("archive", false, "also file the ended session out of the active list")
	asJSON := cmdline.JSONFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	result, err := sessions.Stop(sessionID, *dryRun, *archive)
	if errors.Is(err, sessioncmd.ErrUnmanagedCaller) && *ifManaged {
		return cmdline.Emit(out, *asJSON, sessioncmd.StopResult{DryRun: *dryRun}, "caller is unmanaged")
	}
	if err != nil {
		return err
	}
	verb := "stop"
	if *archive {
		verb = "stop and archive"
	}
	message := "queued " + verb + " for " + sessioncmd.FormatSession(result.Target)
	if *dryRun {
		message = "would " + verb + " " + sessioncmd.FormatSession(result.Target)
	}
	if result.CallerProof != "" {
		message = "Gate Inbox stop proof: " + result.CallerProof + "\nRelay this exact proof line in this conversation; the pinned worker will verify it before acting."
	}
	return cmdline.Emit(out, *asJSON, result, message)
}

func finishStop(args []string, sessionID, configDir string) error {
	set := cmdline.NewFlagSet("_finish-stop [--archive] <launch-time> <pane-id> <process-pin>")
	archive := set.Bool("archive", false, "file the ended session out of the active list")
	proof := set.String("caller-proof", "", "verify this proof in the calling conversation's pane")
	thread := set.String("caller-thread", "", "pin the conversation requesting caller verification")
	dryRun := set.Bool("dry-run", false, "verify without stopping the session")
	operands, err := parseCommand(os.Stdout, set, args, 3, 3)
	if err != nil {
		return err
	}
	if *proof == "" && (*dryRun || *thread != "") {
		return errors.New("stop verification flags require a caller proof")
	}
	// This runs as a run-shell -b job, which tmux SIGTERMs when ending the
	// last session empties the server and leaves its output pipe closed.
	signal.Ignore(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	if *proof != "" {
		_, err := sessioncmd.NewSessions(configDir, sessioncmd.CLIVocabulary()).FinishStopWithProof(sessionID, operands[0], operands[1], operands[2], *thread, *proof, *dryRun, *archive)
		return err
	}
	_, err = sessioncmd.NewSessions(configDir, sessioncmd.CLIVocabulary()).FinishStop(sessionID, operands[0], operands[1], operands[2], *archive)
	return err
}
