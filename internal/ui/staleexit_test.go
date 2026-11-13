package ui

import (
	"os"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// An exit record outlives the agent that wrote it. When the operator starts
// the agent again in the same pane -- /exit, then the CLI from the shell --
// nothing removes it, and a board that believed it read the live agent as
// errored for good: on 2026-11-13 two sessions sat on open questions for an
// hour reading errored, off the queue. A process tree with the agent still in
// it says the record is the last agent's.
func TestAnExitRecordUnderALiveAgentIsNotErrored(t *testing.T) {
	m := buildModel(t)
	m.cfg.Tools["sleeper"] = config.Tool{Command: `sh -c 'echo ready; sleep 60'`, DefaultStatus: status.Idle}
	engine, err := status.NewEngine(m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.poller.engine = engine
	if err := os.MkdirAll(m.hooks.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	sess := store.Session{ID: "relaunch1", Name: "relaunched", Tool: "sleeper", Cwd: t.TempDir(), Status: status.Starting}
	if err := m.store.LaunchSession(sess, func() error {
		return m.tmux.Create(sess.ID, sess.Cwd, m.cfg.Tools["sleeper"].Command,
			map[string]string{hooks.EnvExitFile: m.hooks.ExitFile(sess.ID)}, 80, 24)
	}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(func() { _ = m.tmux.Kill(sess.ID) })
	time.Sleep(300 * time.Millisecond)
	// The first agent quit cleanly; the one running now was started by hand.
	if err := os.WriteFile(m.hooks.ExitFile(sess.ID), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if msg, failed := m.poller.refreshOnce().(errMsg); failed {
			t.Fatalf("refresh: %v", msg.err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	row, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status == status.Errored {
		t.Fatalf("a live agent over an old exit record reads %q", row.Status)
	}
}
