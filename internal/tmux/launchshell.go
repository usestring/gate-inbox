package tmux

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
)

// loginInteractive is what starts an agent in the environment the operator
// gets from opening a terminal. Both halves are load-bearing: the profile
// chain a login shell reads is where the PATH is exported, and the rc chain
// only an interactive one reads is where the aliases and functions are
// defined, so either flag alone leaves half the environment behind.
var loginInteractive = []string{"-l", "-i"}

// loginProbeToken is what a shell has to hand back through an exported
// variable to be trusted with the launch script.
const loginProbeToken = "gi-login-shell-ok"

// loginProbeTimeout bounds a startup-file chain that never returns, so a
// pane still opens on a shell nobody can start.
const loginProbeTimeout = 10 * time.Second

// launchShell is the shell a pane runs its launch script under, and drops to
// once the agent exits.
type launchShell struct {
	path  string
	flags []string
}

func resolveLaunchShell() launchShell {
	path := os.Getenv("SHELL")
	if path == "" {
		return launchShell{path: "/bin/sh"}
	}
	if !runsLoginScript(path) {
		return launchShell{path: path}
	}
	return launchShell{path: path, flags: loginInteractive}
}

// readsStartupFiles reports whether the launch script runs with the operator's
// own environment, which is also the case where startup files can print into
// the pane before the agent has drawn anything.
func (s launchShell) readsStartupFiles() bool {
	return len(s.flags) > 0
}

// runScript is the tmux window command for the launch script. A shell that
// cannot be trusted with it runs under sh, as every pane once did.
func (s launchShell) runScript(script string) string {
	if !s.readsStartupFiles() {
		return "sh " + ShellQuote(script)
	}
	return ShellQuote(s.path) + " " + strings.Join(s.flags, " ") + " -c " + ShellQuote(sourceCommand(script))
}

// sourceCommand reads the script without handing it to the shell as its
// input file. An interactive dash prompts once per line it reads that way,
// and those prompts land in the pane in front of the agent; sourcing prompts
// for nothing, on every shell tried.
func sourceCommand(script string) string {
	return ". " + ShellQuote(script)
}

// execLine is the line the launch script ends on, so the pane stays up on a
// shell the operator can keep working in.
func (s launchShell) execLine() string {
	return strings.Join(append([]string{"exec", ShellQuote(s.path)}, s.flags...), " ")
}

var (
	loginProbeMu      sync.Mutex
	loginProbeResults = map[string]bool{}
)

// runsLoginScript reports whether shell can run a POSIX launch script as a
// login, interactive shell. It asks in exactly the form Create uses, because
// the flags are not the whole question: fish and csh take them and then choke
// on `export`, so the probe round-trips a token through an exported variable
// rather than trusting an exit status. The answer is cached per shell because
// asking costs a whole startup-file chain.
func runsLoginScript(shell string) bool {
	loginProbeMu.Lock()
	defer loginProbeMu.Unlock()
	if answer, asked := loginProbeResults[shell]; asked {
		return answer
	}
	answer := probeLoginScript(shell)
	loginProbeResults[shell] = answer
	logging.Info("tmux launch shell probed", "shell", shell, "login", answer)
	return answer
}

func probeLoginScript(shell string) bool {
	script, err := os.CreateTemp("", "gi-shell-probe-*.sh")
	if err != nil {
		return false
	}
	defer os.Remove(script.Name())
	body := "export GATE_INBOX_LOGIN_SHELL_PROBE=" + loginProbeToken + "\n" +
		"printf '%s\\n' \"$GATE_INBOX_LOGIN_SHELL_PROBE\"\n"
	if _, err := script.WriteString(body); err != nil {
		script.Close()
		return false
	}
	if err := script.Close(); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginProbeTimeout)
	defer cancel()
	args := append(slices.Clone(loginInteractive), "-c", sourceCommand(script.Name()))
	// Stdin stays nil, which the child gets as /dev/null: startup files
	// branch on having a terminal — this operator's replaces the shell with
	// tmux when it finds one — and the probe has to answer no.
	cmd := exec.CommandContext(ctx, shell, args...)
	// A session of its own, with no controlling terminal. The probe is an
	// interactive shell, and an interactive zsh that finds a controlling
	// terminal it is not the foreground of takes it (acquire_pgrp: a group of
	// its own, then tcsetpgrp) and on exit hands it to the group it started
	// in, not the one that had it. This runs from `gate-inbox spawn` too,
	// and the MCP server that calls spawn is a child of the agent whose pane
	// it lives in: the probe left that pane's terminal with the MCP server,
	// and the agent was stopped by SIGTTIN at its next read, which its launch
	// shell reported as `suspended (tty input)` before dropping to a prompt.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// The token, not the exit status, is the verdict: a startup file that
	// ends on a failing command still leaves a usable shell behind.
	out, _ := cmd.Output()
	return strings.Contains(string(out), loginProbeToken)
}
