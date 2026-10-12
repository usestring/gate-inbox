//go:build darwin || linux

package codexbind

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExistingDaemonIsNeverStartedOrRestarted(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "daemon.sock")
	// A file is enough to exercise startup selection; the bridge still verifies the Unix peer.
	if err := os.WriteFile(socket, nil, 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls")
	cli := filepath.Join(dir, "codex")
	info, _ := json.Marshal(map[string]string{"status": "running", "socketPath": socket, "managedCodexPath": cli})
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nprintf '%s\\n' '" + string(info) + "'\n"
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := ensureDaemon(context.Background(), cli, socket)
	if err != nil || got != cli {
		t.Fatalf("identity: %q, %v", got, err)
	}
	calls, _ := os.ReadFile(log)
	if string(calls) != "app-server daemon version\n" {
		t.Fatalf("live daemon mutation: %s", calls)
	}
}

func TestColdDaemonStartFailsWithoutPrivateFallback(t *testing.T) {
	cli := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := ensureDaemon(context.Background(), cli, filepath.Join(t.TempDir(), "missing.sock"))
	if err == nil || !strings.Contains(err.Error(), "start shared Codex daemon") {
		t.Fatalf("startup failure: %v", err)
	}
}

func TestColdDaemonStartDropsTheStartingSession(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "daemon.sock")
	seen := filepath.Join(dir, "environment")
	cli := filepath.Join(dir, "codex")
	info, _ := json.Marshal(map[string]string{"status": "running", "socketPath": socket, "managedCodexPath": cli})
	script := "#!/bin/sh\nif [ \"$3\" = start ]; then env > '" + seen + "'; : > '" + socket + "'; fi\nprintf '%s\\n' '" + string(info) + "'\n"
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATE_INBOX_SESSION_ID", "starter")
	t.Setenv("GATE_INBOX_HOME", dir)
	t.Setenv("GATE_INBOX_BIN", cli)
	t.Setenv(Env, "starter.sock#secret")
	t.Setenv("TMUX", "/tmp/starter,1,0")
	t.Setenv("TMUX_PANE", "%7")
	t.Setenv("KEEP", "value")
	if _, err := ensureDaemon(context.Background(), cli, socket); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	// Failures name the variable only: the dump holds the runner's own secrets.
	kept := false
	for _, kv := range strings.Split(string(raw), "\n") {
		key, value, _ := strings.Cut(kv, "=")
		kept = kept || key == "KEEP" && value == "value"
		if key == "TMUX" || key == "TMUX_PANE" || strings.HasPrefix(key, "GATE_INBOX_") {
			t.Errorf("daemon inherited %s", key)
		}
	}
	if !kept {
		t.Error("daemon lost the operator environment")
	}
}

func TestSocketAliasUsesFileIdentity(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "gi-socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	alias := filepath.Join(dir, "alias.sock")
	if err = os.Symlink(socket, alias); err != nil {
		t.Fatal(err)
	}
	if !sameFile(alias, socket) || sameFile(socket, filepath.Join(dir, "foreign.sock")) {
		t.Fatal("socket identity check failed")
	}
}
