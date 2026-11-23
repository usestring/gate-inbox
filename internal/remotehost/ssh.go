// Package remotehost talks to a Gate Inbox worker over the operator's SSH connection.
package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/tmux"
)

const timeout = 8 * time.Second

// Sessions reads a snapshot without asking for passwords or occupying the terminal.
func Sessions(host config.Host) (sessioncmd.HostSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", arguments(host, "-T", "sessions")...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return sessioncmd.HostSnapshot{}, fmt.Errorf("SSH session list timed out")
	}
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && len(exit.Stderr) > 0 {
			return sessioncmd.HostSnapshot{}, errors.New(strings.TrimSpace(string(exit.Stderr)))
		}
		return sessioncmd.HostSnapshot{}, err
	}
	var snapshot sessioncmd.HostSnapshot
	if err := json.Unmarshal(out, &snapshot); err != nil {
		return sessioncmd.HostSnapshot{}, fmt.Errorf("invalid worker response: %w", err)
	}
	if snapshot.Sessions == nil || snapshot.Matched < len(snapshot.Sessions) {
		return sessioncmd.HostSnapshot{}, fmt.Errorf("invalid worker session list")
	}
	return snapshot, nil
}

// Attach gives the same terminal to the worker's tmux client until it detaches.
func Attach(host config.Host, id string) *exec.Cmd {
	cmd := exec.Command("ssh", arguments(host, "-tt", "attach", id)...)
	// The board logs its own stderr. SSH's diagnostics go to the terminal
	// while the attach process has control of it.
	cmd.Stderr = os.Stdout
	return cmd
}

func arguments(host config.Host, tty string, operands ...string) []string {
	bin := host.Binary
	if bin == "" {
		bin = "gate-inbox"
	}
	command := "exec "
	if host.Home != "" {
		command += "env " + tmux.ShellQuote(config.HomeEnv+"="+host.Home) + " "
	}
	command += tmux.ShellQuote(bin) + " host"
	// OpenSSH joins the remote argv for the worker's shell. Quote each
	// operand, including session IDs received from the worker's snapshot.
	for _, arg := range operands {
		command += " " + tmux.ShellQuote(arg)
	}
	return []string{
		tty, "-o", "BatchMode=yes", "-o", "ConnectTimeout=4",
		"-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2",
		"-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no",
		"--", host.SSH, command,
	}
}
