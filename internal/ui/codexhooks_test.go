package ui

import (
	"os"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const codexTestThread = "0190a000-0000-7000-8000-00000000000a"

// codexModel is buildModel with quietchat, a tool whose status comes off its
// pane, standing in for codex: a session store of "codex" is what marks a
// tool's rows as codex threads.
func codexModel(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	m.poller.sessionStores["quietchat"] = "codex"
	return m
}

// A codex row's hook events count for codexHookFresh: a turn's start reads
// as working over a pane that shows nothing yet, its end as finished, and
// once the events are old the pane decides again.
func TestCodexHookEventsDriveAFreshTurn(t *testing.T) {
	m := codexModel(t)
	sess := store.Session{ID: "codexrow1", Tool: "quietchat", Status: status.Idle, TmuxPaneID: "%3"}
	pane := "› hello\n› \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Idle {
		t.Fatalf("no hook events = %q, want idle", got)
	}
	appendHookLog(t, m, sess.ID, "working UserPromptSubmit\n")
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("a fresh UserPromptSubmit = %q, want working", got)
	}
	sess.Status = status.Working
	appendHookLog(t, m, sess.ID, "finished Stop\n")
	if got := deriveStatus(t, m, sess, pane, true); got != status.Finished {
		t.Fatalf("a fresh Stop = %q, want finished", got)
	}

	appendHookLog(t, m, sess.ID, "working UserPromptSubmit\n")
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(m.hooks.StatusFile(sess.ID), old, old); err != nil {
		t.Fatal(err)
	}
	sess.Status = status.Idle
	deriveStatus(t, m, sess, pane, true)
	if got := deriveStatus(t, m, sess, pane, true); got != status.Idle {
		t.Fatalf("a stale working event = %q, want the pane's idle", got)
	}
}

// A tool that is not codex never reads a hook file, whatever is in it.
func TestCodexHookEventsIgnoredForOtherTools(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "plainrow1", Tool: "quietchat", Status: status.Idle}
	appendHookLog(t, m, sess.ID, "working UserPromptSubmit\n")
	if got := deriveStatus(t, m, sess, "› hello\n› \n", true); got != status.Idle {
		t.Fatalf("a non-codex row = %q, want idle", got)
	}
}

// The poller publishes every live codex row for the hooks -- adopted ones
// with their pane, launched ones once their thread is known -- and nothing
// for dead, archived or other rows.
func TestPollerPublishesCodexRows(t *testing.T) {
	m := codexModel(t)
	sessions := []store.Session{
		{ID: "adopted1", Tool: "quietchat", Status: status.Idle, TmuxSocket: "/s", TmuxPaneID: "%1", Cwd: "/w"},
		{ID: "launched1", Tool: "quietchat", Status: status.Working, AgentSessionID: codexTestThread},
		{ID: "launched2", Tool: "quietchat", Status: status.Working},
		{ID: "dead1", Tool: "quietchat", Status: status.Dead, TmuxPaneID: "%2"},
		{ID: "claude1", Tool: "claude", Status: status.Idle, TmuxPaneID: "%4"},
	}
	panes := map[string]int{"adopted1": 10, "launched1": 11, "launched2": 12, "dead1": 13, "claude1": 14}
	m.poller.syncCodexHooks(sessions, panes, time.Now())
	if id, adopted, ok := m.hooks.CodexThreadRow(codexTestThread); !ok || id != "launched1" || adopted {
		t.Fatalf("launched thread = %q adopted=%v ok=%v", id, adopted, ok)
	}
	if _, err := os.Stat(hooks.CodexWatchFile(m.hooks.ConfigDir())); err != nil {
		t.Fatal("no watch file with codex rows on the board")
	}
	m.poller.syncCodexHooks(nil, nil, time.Now())
	if _, err := os.Stat(hooks.CodexWatchFile(m.hooks.ConfigDir())); err == nil {
		t.Fatal("the watch file outlived the board's codex rows")
	}
}
