package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/mcpserver"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

// RunHook is the "hook" subcommand Claude Code's hooks call. It is plumbing,
// not a command an agent runs, so it is left out of the help. It always
// exits 0: a hook that fails must not stand in the child's way.
func RunHook(in io.Reader, out io.Writer, args []string, sessionID, configDir string) error {
	if len(args) == 2 && args[0] == "codex" {
		// A codex hook runs in codex's shared daemon, whose environment --
		// a session id included -- is whichever pane started it. Only the
		// payload's thread says which session this is.
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil {
			return nil
		}
		output := hooks.NewManager(configDir).DispatchCodex(args[1], payload, adopt.Capture, codexSteering)
		if output != "" {
			fmt.Fprintln(out, output)
		}
		return nil
	}
	if len(args) == 2 && args[0] == "global" {
		payload, err := io.ReadAll(io.LimitReader(in, 1<<20))
		if err != nil || sessionID == "" {
			return nil
		}
		m := hooks.NewManager(configDir)
		_ = m.RecordConversation(sessionID, payload)
		output := m.DispatchGlobal(args[1], payload)
		// An adopted claude's first prompt also carries the board's standing
		// instructions, which reached a launched one at startup.
		agentPID, _ := strconv.Atoi(os.Getenv(envname.AgentPID))
		output = hooks.MergeHookOutputs(output, m.AdoptedSteering(args[1], sessionID, agentPID, mcpserver.AdoptedInstructions))
		// Its grants ride these hooks too, since it never loaded the
		// settings file a launch carries them in. A refusal already made,
		// of the sealing keys, stands.
		switch args[1] {
		case "PreToolUse":
			if !hooks.OutputSays(output, "permissionDecision") {
				output = hooks.MergeHookOutputs(output, sessioncmd.AdoptedGrantDecision(configDir, sessionID, payload, time.Now()))
			}
		case "PostToolUse":
			if !hooks.OutputSays(output, "classifierContext") {
				output = hooks.MergeHookOutputs(output, sessioncmd.AdoptedSoftNote(configDir, sessionID, agentPID, payload, time.Now()))
			}
		}
		if output != "" {
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

// codexSteering is what an adopted codex hears once: it has no Gate Inbox
// MCP server, so the board's steering is written for the gate-inbox command.
func codexSteering() string { return mcpreg.AdoptedCLISteering("codex") }
