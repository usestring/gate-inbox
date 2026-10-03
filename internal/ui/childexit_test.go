package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestChildExitMessageNamesTheChildTheExitAndTheScrubbedTail(t *testing.T) {
	key := "sk-" + strings.Repeat("a1", 12)
	pane := "\x1b[31mError: unexpected argument '--nope' found\x1b[0m\n\nOPENAI_API_KEY=" + key + "\nUsage: codex [OPTIONS]\n\n$ \n\n"
	body := childExitMessage(store.Session{ID: "child001", Name: "census"}, classifyExit(2), pane)
	for _, want := range []string{
		"census", "child001", "has exited", "exit status 2",
		"  | Error: unexpected argument '--nope' found",
		"  | Usage: codex [OPTIONS]",
		"send_session cannot reach it", "revive_session",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exit relay does not carry %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, key) || strings.Contains(body, "\x1b[") {
		t.Errorf("exit relay leaked a credential or an escape:\n%s", body)
	}
}

func TestPaneTailKeepsTheLastNonBlankLines(t *testing.T) {
	var lines []string
	for i := range 20 {
		lines = append(lines, "line "+string(rune('a'+i)), "")
	}
	got := strings.Split(paneTail(strings.Join(lines, "\n"), 3), "\n")
	if strings.Join(got, ",") != "line r,line s,line t" {
		t.Fatalf("paneTail = %q", got)
	}
}

// A child whose CLI exits leaves its pane on a shell, which matches no rule
// and used to read as the tool's default status: idle. The launch script's exit
// record is what says the agent has gone.
func TestAChildWhoseAgentExitsReadsErroredAndItsSpawnerIsTold(t *testing.T) {
	m := buildModel(t)
	m.cfg.Tools["crasher"] = config.Tool{Command: `sh -c 'echo "fatal: unknown flag --nope"; exit 3'`, DefaultStatus: status.Idle}
	engine, err := status.NewEngine(m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.poller.engine = engine
	if err := os.MkdirAll(m.hooks.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.spawnSession("claude", "parent", t.TempDir(), "", "", false); err != nil {
		t.Fatalf("spawn parent: %v", err)
	}
	parent := m.sessionRows()[0]
	child := store.Session{ID: "exitkid1", Name: "exit-kid", Tool: "crasher", Cwd: t.TempDir(),
		Group: parent.Group, ParentID: parent.ID, SpawnedBy: parent.ID, Status: status.Starting}
	if err := m.store.LaunchSession(child, func() error {
		return m.tmux.Create(child.ID, child.Cwd, m.cfg.Tools["crasher"].Command,
			map[string]string{hooks.EnvExitFile: m.hooks.ExitFile(child.ID)}, 80, 24)
	}); err != nil {
		t.Fatalf("launch child: %v", err)
	}
	t.Cleanup(func() { _ = m.tmux.Kill(child.ID) })

	deadline := time.Now().Add(10 * time.Second)
	var row store.Session
	for {
		if msg, failed := m.poller.refreshOnce().(errMsg); failed {
			t.Fatalf("refresh: %v", msg.err)
		}
		var err error
		if row, err = m.store.Get(child.ID); err != nil {
			t.Fatal(err)
		}
		if row.Status == status.Errored || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if row.Status != status.Errored {
		t.Fatalf("an exited child reads %q, want %q", row.Status, status.Errored)
	}
	if !m.tmux.Exists(child.ID) {
		t.Fatal("the fixture needs the pane to outlive the agent")
	}
	head, found, err := m.store.HeadMessage(parent.ID)
	if err != nil || !found {
		t.Fatalf("the spawner was told nothing: found=%v err=%v", found, err)
	}
	for _, want := range []string{"exit-kid", "has exited", "exit status 3", "fatal: unknown flag --nope"} {
		if !strings.Contains(head.Body, want) {
			t.Errorf("relay does not carry %q:\n%s", want, head.Body)
		}
	}
	for range 3 {
		if msg, failed := m.poller.refreshOnce().(errMsg); failed {
			t.Fatalf("refresh: %v", msg.err)
		}
	}
	if row, _ = m.store.Get(child.ID); row.Status != status.Errored {
		t.Fatalf("the exited child drifted to %q", row.Status)
	}
}
