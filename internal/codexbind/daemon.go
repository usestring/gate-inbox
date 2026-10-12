//go:build darwin || linux

package codexbind

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

func ensureDaemon(ctx context.Context, codex, socket string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := os.Stat(socket); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, codex, "app-server", "daemon", "start")
		cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
		if out, err := cmd.Output(); err != nil {
			return "", fmt.Errorf("start shared Codex daemon: %w: %s", err, out)
		}
	} else if err != nil {
		return "", fmt.Errorf("inspect Codex daemon socket: %w", err)
	}
	out, err := exec.CommandContext(ctx, codex, "app-server", "daemon", "version").Output()
	if err != nil {
		return "", fmt.Errorf("read Codex daemon identity: %w", err)
	}
	var info struct {
		Status     string `json:"status"`
		Socket     string `json:"socketPath"`
		Executable string `json:"managedCodexPath"`
	}
	if json.Unmarshal(out, &info) != nil || info.Status != "running" || !sameFile(info.Socket, socket) || !filepath.IsAbs(info.Executable) {
		return "", fmt.Errorf("native Codex daemon did not report a running local executable")
	}
	return info.Executable, nil
}

func (b *bridge) verifiedDaemonExecutable(pid int) bool {
	if b.daemonExecutable == "" {
		return sameProcessExecutable(pid, b.identity.Client.PID)
	}
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return false
	}
	path, err := p.Exe()
	if err != nil {
		return false
	}
	actual, err := os.Stat(path)
	if err != nil {
		return false
	}
	expected, err := os.Stat(b.daemonExecutable)
	return err == nil && os.SameFile(actual, expected)
}

func sameFile(first, second string) bool {
	a, err := os.Stat(first)
	if err != nil {
		return false
	}
	b, err := os.Stat(second)
	return err == nil && os.SameFile(a, b)
}
