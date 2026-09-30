package cli

import (
	"fmt"
	"io"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

// RunHook is the "hook" subcommand Claude Code's hooks call. It is plumbing,
// not a command an agent runs, so it is left out of the help. It always
// exits 0: a hook that fails must not stand in the child's way.
func RunHook(in io.Reader, out io.Writer, args []string, sessionID, configDir string) error {
	if len(args) != 1 {
		return nil
	}
	if args[0] == "session-start" {
		if note := sessioncmd.SessionStartHook(configDir, sessionID); note != "" {
			fmt.Fprintln(out, note)
		}
		return nil
	}
	if args[0] != "ask-answered" {
		return nil
	}
	payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil {
		return nil
	}
	if note := sessioncmd.AskAnsweredHook(configDir, sessionID, payload); note != "" {
		fmt.Fprintln(out, note)
	}
	return nil
}
