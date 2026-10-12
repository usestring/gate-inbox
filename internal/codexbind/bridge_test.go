//go:build darwin || linux

package codexbind

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Run(m.Run))
}

func TestThreadConfigurationCarriesFreshCapability(t *testing.T) {
	b := &bridge{identity: Identity{Home: t.TempDir()}, launches: map[string]string{}, dir: t.TempDir()}
	raw := []byte(`{"id":1,"method":"thread/start","params":{"config":{"shell_environment_policy":{"set":{"KEEP":"value","GATE_INBOX_CALLER_BINDING":"foreign"}}}}}`)
	data, err := b.prepare(raw, "/tmp/fixture/client.sock")
	if err != nil {
		t.Fatal(err)
	}
	var m message
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	set := m.Params["config"].(map[string]any)["shell_environment_policy"].(map[string]any)["set"].(map[string]any)
	if set["KEEP"] != "value" || set[Env] != b.launches["1"] || len(b.launches["1"]) < 64 || set["GATE_INBOX_SESSION_ID"] != "" {
		t.Fatalf("runtime configuration: %v", set)
	}
	if _, err = b.prepare(raw, "/tmp/fixture/client.sock"); err == nil {
		t.Fatal("accepted ambiguous concurrent thread start")
	}
}

func TestRuntimeEventsRevokeCompletedCommandProof(t *testing.T) {
	b := &bridge{}
	params := map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]any{"id": "call", "type": "commandExecution", "command": "sh -c 'sleep 60'"}}
	b.observe(message{Method: "item/started", Params: params})
	if len(b.tools) != 1 {
		t.Fatal("active runtime command unavailable")
	}
	b.observe(message{Method: "item/completed", Params: params})
	if len(b.tools) != 0 {
		t.Fatal("completed command retained caller authority")
	}
	b.observe(message{Method: "item/started", Params: params})
	b.observe(message{Method: "turn/completed", Params: map[string]any{"threadId": "thread"}})
	if len(b.tools) != 0 {
		t.Fatal("completed turn retained caller authority")
	}
}

func TestIdenticalDaemonCommandsAreAmbiguous(t *testing.T) {
	launch := func() toolLaunch {
		t.Helper()
		cmd := exec.Command("sleep", "60")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		pin, err := ProcessPin(cmd.Process.Pid)
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if pin.Live() {
				cmd.Process.Kill()
			}
			cmd.Wait()
		})
		return toolLaunch{pin, []string{"sleep", "60"}}
	}
	first := launch()
	if !uniqueLaunch(first, os.Getpid()) {
		t.Fatal("unique private tool launch refused")
	}
	launch()
	if uniqueLaunch(first, os.Getpid()) {
		t.Fatal("identical parallel daemon tools acquired ambiguous authority")
	}
}

func TestDuplicateResumeRefusesBeforeChangingRuntimeConfiguration(t *testing.T) {
	dir := t.TempDir()
	first := &bridge{dir: dir, identity: Identity{Daemon: Pin{PID: 123, Started: 456}}}
	second := &bridge{dir: dir, identity: first.identity, launches: map[string]string{}}
	if err := first.claim("thread"); err != nil {
		t.Fatal(err)
	}
	defer first.lock.Close()
	if _, err := second.prepare([]byte(`{"id":1,"method":"thread/resume","params":{"threadId":"thread"}}`), "/tmp/fixture/client.sock"); err == nil {
		t.Fatal("second client acquired the same daemon thread")
	}
	if len(second.launches) != 0 || second.token != "" {
		t.Fatal("duplicate resume created a capability")
	}
	if err := first.claim("thread"); err != nil {
		t.Fatal("same client could not resume its own thread")
	}
}

func TestRecycledProcessStartIdentityRefuses(t *testing.T) {
	pin, err := ProcessPin(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !pin.Live() {
		t.Fatal("fixture process unavailable")
	}
	pin.Started--
	if pin.Live() {
		t.Fatal("accepted recycled PID")
	}
	if Descends(os.Getpid(), -1) {
		t.Fatal("accepted nonexistent ancestor")
	}
}

func TestDisconnectLetsTheSameClientReconnect(t *testing.T) {
	b := &bridge{identity: Identity{Home: t.TempDir()}, launches: map[string]string{}, dir: t.TempDir(), busy: true, connected: true, token: "stale"}
	start := []byte(`{"id":1,"method":"thread/start","params":{}}`)
	if _, err := b.prepare(start, "/tmp/fixture/client.sock"); err != nil {
		t.Fatal(err)
	}
	b.observe(message{Method: "item/started", Params: map[string]any{"threadId": "thread", "turnId": "turn", "item": map[string]any{"id": "call", "type": "commandExecution"}}})
	b.disconnect()
	if b.busy || b.connected || b.token != "" || len(b.tools) != 0 {
		t.Fatal("closed connection retained its slot or caller authority")
	}
	if _, err := b.prepare(start, "/tmp/fixture/client.sock"); err != nil {
		t.Fatalf("reconnected client could not start a thread: %v", err)
	}
}

func TestEphemeralChecksDoNotRevokeVisibleThread(t *testing.T) {
	b := &bridge{token: "visible-token", identity: Identity{Thread: "visible-thread"}, launches: map[string]string{}}
	request := []byte(`{"id":1,"method":"thread/start","params":{"ephemeral":true}}`)
	data, err := b.prepare(request, "unused")
	if err != nil || string(data) != string(request) || b.token != "visible-token" || b.identity.Thread != "visible-thread" || len(b.launches) != 0 {
		t.Fatal("structured check changed the visible session identity")
	}
}

func TestNewThreadUsesTheClientDirectory(t *testing.T) {
	for _, cwd := range []any{nil, "/tmp/explicit"} {
		b := &bridge{workingDirectory: "/tmp/client", identity: Identity{Home: t.TempDir()}, launches: map[string]string{}}
		raw, _ := json.Marshal(message{ID: json.RawMessage("1"), Method: "thread/start", Params: map[string]any{"cwd": cwd}})
		data, err := b.prepare(raw, "/tmp/client.sock")
		if err != nil {
			t.Fatal(err)
		}
		var m message
		json.Unmarshal(data, &m)
		want := cwd
		if want == nil {
			want = "/tmp/client"
		}
		if m.Params["cwd"] != want {
			t.Fatalf("cwd = %v, want %v", m.Params["cwd"], want)
		}
	}
}
