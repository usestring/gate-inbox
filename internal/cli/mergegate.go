// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/mergegate"
)

const usageMergeGateCheck = `merge-gate-check --command "<text>" [--cwd <dir>]`

// RunMergeGateCheck is the merge-gate mod's way back into the Go gate: it
// exits zero, printing why, when the standing merge authority covers
// command, and non-zero otherwise. Every refusal fails closed, leaving the
// merge to Claude Code's own permission flow.
func RunMergeGateCheck(out io.Writer, args []string, configDir string) error {
	set := cmdline.NewFlagSet(usageMergeGateCheck)
	command := set.String("command", "", "the Bash command the mod is asked about")
	cwd := set.String("cwd", "", "the session's working directory; defaults to this process's")
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	return checkMergeGate(out, *command, *cwd, configDir)
}

func checkMergeGate(out io.Writer, command, cwd, configDir string) error {
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return fmt.Errorf("merge gate: cannot load config: %w", err)
	}
	if !cfg.MergeGate.Enabled() {
		return fmt.Errorf("merge gate: no standing merge authority is configured")
	}
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("merge gate: cannot tell the working directory: %w", err)
		}
	}
	m, ok := mergegate.Parse(command, cwd)
	if !ok {
		return fmt.Errorf("merge gate: the standing merge authority does not cover that shape of command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := mergegate.Check(ctx, cfg.MergeGate, m); err != nil {
		return fmt.Errorf("merge gate: %w", err)
	}
	_, err = fmt.Fprintf(out, "Gate Inbox merge gate: the standing merge authority covers a squash merge of #%d, "+
		"an open, green, non-draft pull request into its repository's default branch.\n", m.Number)
	return err
}
