package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/usestring/gate-inbox/internal/agentnudge"
	"github.com/usestring/gate-inbox/internal/convo"
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
		m := hooks.NewManager(configDir)
		_ = m.RecordConversation(sessionID, payload)
		if output := m.DispatchGlobal(args[1], payload); output != "" {
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
	case "child-dialogs":
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil {
			return nil
		}
		if note := sessioncmd.ChildNudgeHook(configDir, convo.ClaudeHome(), sessionID, statusSibling(sessioncmd.ChildNudgeSuffix),
			sessioncmd.ChildSegmentDir(), payload, time.Now()); note != "" {
			fmt.Fprintln(out, note)
		}
	case "sandbox-stale":
		flag := statusSibling(hooks.SandboxStaleSuffix)
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if flag == "" || err != nil {
			return nil
		}
		if note := sessioncmd.SandboxStaleHook(configDir, sessionID, flag, payload, time.Now()); note != "" {
			fmt.Fprintln(out, note)
		}
	case "agent-nudge":
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil || os.Getenv(envname.StatusFile) == "" {
			return nil
		}
		home, _ := os.UserHomeDir()
		paths := agentnudge.Paths{Dir: agentnudge.StateDir(configDir), SessionID: sessionID, Home: home}
		if _, note := agentnudge.Hook(payload, paths, time.Now()); note != "" {
			fmt.Fprintln(out, note)
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

func attestFlag() string { return statusSibling(hooks.AttestPendingSuffix) }

func statusSibling(suffix string) string {
	status := os.Getenv(envname.StatusFile)
	if status == "" {
		return ""
	}
	return status + suffix
}
