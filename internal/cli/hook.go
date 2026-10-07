package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

// RunHook is the "hook" subcommand Claude Code's hooks call. It is plumbing,
// not a command an agent runs, so it is left out of the help. It always
// exits 0: a hook that fails must not stand in the child's way.
func RunHook(in io.Reader, out io.Writer, args []string, sessionID, configDir string) error {
	if len(args) == 2 && args[0] == "global" {
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil || sessionID == "" {
			return nil
		}
		if output := hooks.NewManager(configDir).DispatchGlobal(args[1], payload); output != "" {
			fmt.Fprintln(out, output)
		}
		return nil
	}
	if len(args) != 1 {
		return nil
	}
	switch args[0] {
	case "session-start":
		if note := sessioncmd.SessionStartHook(configDir, sessionID); note != "" {
			fmt.Fprintln(out, note)
		}
	case "ask-answered":
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil {
			return nil
		}
		if note := sessioncmd.AskAnsweredHook(configDir, sessionID, payload); note != "" {
			fmt.Fprintln(out, note)
		}
	case "ask-pending":
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil {
			return nil
		}
		sessioncmd.AskPendingHook(configDir, sessionID, payload)
	case "prompt-submit":
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil {
			return nil
		}
		note, attested := sessioncmd.PromptSubmitHook(configDir, sessionID, payload, time.Now())
		if attested {
			if flag := attestFlag(); flag != "" {
				_ = os.WriteFile(flag, nil, 0o600)
			}
		}
		if note != "" {
			fmt.Fprintln(out, note)
		}
	case "slack-footer":
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil {
			return nil
		}
		if output := hooks.SlackFooterInput(payload); output != "" {
			fmt.Fprintln(out, output)
		}
	case "attest-note":
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil {
			return nil
		}
		note, done := sessioncmd.AttestNoteHook(configDir, sessionID, payload, time.Now())
		if done {
			if flag := attestFlag(); flag != "" {
				_ = os.Remove(flag)
			}
		}
		if note != "" {
			fmt.Fprintln(out, note)
		}
	}
	return nil
}

func attestFlag() string {
	status := os.Getenv(envname.StatusFile)
	if status == "" {
		return ""
	}
	return status + hooks.AttestPendingSuffix
}
