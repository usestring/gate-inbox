package mcprelay

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// The relay in front of the real `gate-inbox mcp`: a session whose pane the
// board marks as adopted gets the board's tools without restarting, and loses
// them when the marker goes.
func TestServeGivesAnAdoptedSessionTheBoardsTools(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "gate-inbox")
	build := exec.Command("go", "build", "-o", bin, "../..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	configDir := t.TempDir()
	t.Setenv(hooks.EnvSessionID, "")
	t.Setenv(hooks.EnvStatusFile, "")
	t.Setenv("TMUX", "/run/tmux-test/default,4242,0")
	t.Setenv("TMUX_PANE", "%7")
	// The relay's parent stands in for the claude the marker names.
	manager := hooks.NewManager(configDir)
	adopt := func(on bool) {
		t.Helper()
		var panes []hooks.AdoptedPane
		if on {
			panes = append(panes, hooks.AdoptedPane{ID: "a1b2c3d4", ServerPID: 4242, PaneID: "%7", AgentPID: os.Getppid()})
		}
		if err := manager.SyncAdopted(panes); err != nil {
			t.Fatal(err)
		}
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(inR, outW, configDir, bin, "test"); outW.Close() }()
	t.Cleanup(func() {
		inW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not end when stdin closed")
		}
	})
	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(outR)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	send := func(line string) {
		if _, err := inW.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	read := func(within time.Duration) map[string]any {
		t.Helper()
		select {
		case line := <-lines:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				t.Fatalf("relay wrote %q", line)
			}
			return msg
		case <-time.After(within):
			t.Fatal("the relay wrote nothing")
			return nil
		}
	}
	tools := func(id int) []string {
		t.Helper()
		raw, _ := json.Marshal(id)
		send(`{"jsonrpc":"2.0","id":` + string(raw) + `,"method":"tools/list"}`)
		msg := read(10 * time.Second)
		var names []string
		for _, tool := range msg["result"].(map[string]any)["tools"].([]any) {
			names = append(names, tool.(map[string]any)["name"].(string))
		}
		return names
	}

	send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"2"}}}`)
	read(5 * time.Second)
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if names := tools(1); len(names) != 0 {
		t.Fatalf("tools before adoption = %v", names)
	}

	adopt(true)
	if msg := read(15 * time.Second); msg["method"] != "notifications/tools/list_changed" {
		t.Fatalf("after adoption the relay wrote %v", msg)
	}
	names := tools(2)
	for _, want := range []string{"send_session", "read_session", "answer_session", "list_sessions", "create_session"} {
		if !strings.Contains(" "+strings.Join(names, " ")+" ", " "+want+" ") {
			t.Fatalf("adopted tools = %v, missing %s", names, want)
		}
	}

	adopt(false)
	if msg := read(10 * time.Second); msg["method"] != "notifications/tools/list_changed" {
		t.Fatalf("after the marker went the relay wrote %v", msg)
	}
	if names := tools(3); len(names) != 0 {
		t.Fatalf("tools after the marker went = %v", names)
	}
}
