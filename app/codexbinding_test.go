//go:build darwin || linux

package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/codexbind"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestSharedCodexBindingStopsOnlyItsAdoptedPane(t *testing.T) {
	bin := buildFixture(t)
	socket := tmuxtest.Socket(t, "boundcodex")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+promptTool+"\n[tools.work-codex]\ncommand = \"codex\"\nsession_store = \"codex\"\n")
	home := envValue(env, "GATE_INBOX_HOME")
	root := t.TempDir()
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	fake := filepath.Join(t.TempDir(), "codex")
	build := exec.Command("go", "build", "-o", fake, "./testdata/boundcodex")
	build.Env = tmuxtest.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	// The socket uses a short private path independently of Go's long test path.
	short, err := os.MkdirTemp("/tmp", "gi-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	server := filepath.Join(short, "daemon.sock")
	cmd := exec.Command(fake, "app-server", server, root, bin)
	cmd.Env = append(withoutKey(withoutKey(env, "GATE_INBOX_SESSION_ID"), "GATE_INBOX_BIN"), "TMUX_PANE=%999999")
	log, err := os.Create(filepath.Join(root, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	daemonPin, err := codexbind.ProcessPin(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if daemonPin.Live() {
			cmd.Process.Kill()
		}
		cmd.Wait()
		log.Close()
	})
	waitBinding(t, func() bool { _, err := os.Stat(server); return err == nil })
	tmuxCmd := func(args ...string) string {
		t.Helper()
		command := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		command.Env = env
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	launch := func(id, thread, stored, tool string) string {
		command := "env -u GATE_INBOX_SESSION_ID -u GATE_INBOX_BIN GATE_INBOX_HOME=" + tmux.ShellQuote(home) + " " + tmux.ShellQuote(bin) + " codex-client --server " + tmux.ShellQuote(server) + " -- " + tmux.ShellQuote(fake) + " " + tmux.ShellQuote(thread)
		tmuxCmd("new-session", "-d", "-s", "gi_"+id, command)
		pane := tmuxCmd("display-message", "-p", "-t", "gi_"+id, "#{pane_id}")
		row := store.Session{ID: id, Name: thread, Tool: tool, Status: "working", CreatedAt: time.Now(), TmuxSocket: socket, TmuxPaneID: pane, AgentSessionID: stored}
		if err := st.CreateSession(row); err != nil {
			t.Fatal(err)
		}
		waitBinding(t, func() bool { _, err := os.Stat(filepath.Join(root, thread+".ready")); return err == nil })
		return pane
	}
	target := launch("bound001", "target-thread", "foreign-stored-thread", "work-codex")
	sentinel := launch("bound002", "sentinel-thread", "target-thread", "codex")
	var set map[string]string
	data, err := os.ReadFile(filepath.Join(root, "target-thread.ready"))
	if err != nil || json.Unmarshal(data, &set) != nil {
		t.Fatalf("runtime config: %s, %v", data, err)
	}
	trigger := func(args []string, overrides map[string]string) string {
		t.Helper()
		result := filepath.Join(root, "target-thread.result")
		os.Remove(result)
		data, _ := json.Marshal(map[string]any{"Args": args, "Env": overrides})
		if err := os.WriteFile(filepath.Join(root, "target-thread.go"), data, 0600); err != nil {
			t.Fatal(err)
		}
		return readEventually(t, result)
	}
	var foreign map[string]string
	data, err = os.ReadFile(filepath.Join(root, "sentinel-thread.ready"))
	if err != nil || json.Unmarshal(data, &foreign) != nil {
		t.Fatal("sentinel capability unavailable")
	}
	for _, overrides := range []map[string]string{
		{codexbind.Env: set[codexbind.Env][:len(set[codexbind.Env])-1] + "x"},
		{"CODEX_THREAD_ID": "sentinel-thread"},
		{codexbind.Env: foreign[codexbind.Env], "CODEX_THREAD_ID": "sentinel-thread"},
		{"GATE_INBOX_SESSION_ID": "bound002"},
	} {
		out := trigger([]string{"--if-managed", "--json"}, overrides)
		if !strings.Contains(out, `"failed":true`) {
			t.Fatalf("spoof accepted: %s", out)
		}
		ends, err := st.SessionEnds()
		if err != nil || len(ends) != 0 {
			t.Fatalf("refusal recorded lifecycle: %v, %v", ends, err)
		}
		if _, err := os.Stat(filepath.Join(root, "interrupts")); !os.IsNotExist(err) {
			t.Fatal("refusal interrupted runtime")
		}
		if !daemonPin.Live() {
			t.Fatal("refusal signalled daemon")
		}
	}
	alias, err := st.Get("bound001")
	if err != nil {
		t.Fatal(err)
	}
	alias.ID = "bound003"
	if err = st.CreateSession(alias); err != nil {
		t.Fatal(err)
	}
	if out := trigger([]string{"--if-managed", "--json"}, nil); !strings.Contains(out, `"failed":true`) {
		t.Fatalf("ambiguous rows accepted: %s", out)
	}
	if ends, _ := st.SessionEnds(); len(ends) != 0 {
		t.Fatal("ambiguous row created a lifecycle record")
	}
	if _, err = os.Stat(filepath.Join(root, "interrupts")); !os.IsNotExist(err) {
		t.Fatal("ambiguous row interrupted a thread")
	}
	if err = st.Delete(alias.ID); err != nil {
		t.Fatal(err)
	}
	dry := trigger([]string{"--if-managed", "--dry-run", "--json"}, nil)
	if !strings.Contains(dry, `\"managed\": true`) || strings.Contains(dry, `"failed":true`) {
		t.Fatalf("verified daemon caller refused: %s", dry)
	}
	trigger([]string{"--if-managed", "--json"}, nil)
	waitBinding(t, func() bool {
		ends, err := st.SessionEnds()
		return err == nil && ends["bound001"].Reason == store.EndKilled
	})
	kept, err := st.Get("bound001")
	if err != nil || kept.Archived || kept.Status != "dead" || kept.AgentSessionID != "foreign-stored-thread" {
		t.Fatalf("history: %+v, %v", kept, err)
	}
	if got := tmuxCmd("list-panes", "-a", "-F", "#{pane_id}"); strings.Contains(got, target) || !strings.Contains(got, sentinel) {
		t.Fatalf("wrong panes survived: %s", got)
	}
	if got := readEventually(t, filepath.Join(root, "interrupts")); got != "target-thread\n" {
		t.Fatalf("wrong thread interrupted: %s", got)
	}
	if !daemonPin.Live() {
		t.Fatal("shared server stopped")
	}
	if got := readEventually(t, filepath.Join(home, "extensions", "noop", "kills.txt")); got != "bound001 cli bound001 dead\n" {
		t.Fatalf("observer: %s", got)
	}
	// A capability whose bridge disappeared is revoked, even if pane ids,
	// conversation matches and shell snapshots are reused by another client.
	out := trigger([]string{"--if-managed", "--json"}, nil)
	if !strings.Contains(out, `"failed":true`) {
		t.Fatalf("stale bridge accepted: %s", out)
	}
}

func waitBinding(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("private binding fixture did not become ready")
}
