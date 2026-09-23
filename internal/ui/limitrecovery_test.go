package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func configureLimitCodex(t testing.TB, m *Model) {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	tool := cfg.Tools["codex"]
	tool.Command = "sh -c " + tmux.ShellQuote("exec cat"+soleWriter)
	tool.ScrolledLine = `Jump to bottom`
	m.cfg.Tools["codex"] = tool
	engine, err := status.NewEngine(m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.poller.engine = engine
}

func recoverLimit(t *testing.T, p *poller, sess store.Session, pane, derived string, alive, available bool, now time.Time) bool {
	t.Helper()
	states, err := p.store.LimitRecoveries()
	if err != nil {
		t.Fatal(err)
	}
	sent, err := p.maybeRecoverLimit(sess, states, tmux.Capture{Text: pane}, ansi.Strip(pane), derived, alive, available, now)
	if err != nil {
		t.Fatal(err)
	}
	return sent
}

func TestLimitRecoveryWaitsThenResumesOnceAcrossManagerRestart(t *testing.T) {
	m := buildModel(t)
	configureLimitCodex(t, m)
	sess := spawnedSession(t, m, "codex")
	observed := time.Date(2026, 9, 6, 20, 30, 0, 0, time.UTC)
	pane := "You've hit your usage limit. Try again at 9pm.\n\n› "
	if recoverLimit(t, m.poller, sess, pane, status.Errored, true, true, observed) {
		t.Fatal("sent before reset")
	}
	states, err := m.store.LimitRecoveries()
	if err != nil {
		t.Fatal(err)
	}
	deadline := states[sess.ID].ResetAt.Add(limitResetGrace)
	if recoverLimit(t, m.poller, sess, pane, status.Errored, true, true, deadline.Add(-time.Nanosecond)) {
		t.Fatal("sent before grace elapsed")
	}
	p := newPoller(m.store, m.tmux, m.poller.engine, m.poller.hooks, nil, m.poller.statusSources, nil, nil, nil, time.Second)
	if !recoverLimit(t, p, sess, pane, status.Errored, true, true, deadline) {
		t.Fatal("did not resume after reset")
	}
	settledPane(t, m, sess.ID, "reported usage-limit reset time", "preserve existing approval requirements")
	if recoverLimit(t, m.poller, sess, pane, status.Errored, true, true, deadline.Add(24*time.Hour)) {
		t.Fatal("retried unchanged banner")
	}
	if recoverLimit(t, p, sess, "A new answer\n› ", status.Working, true, true, deadline) {
		t.Fatal("sent during work")
	}
	states, err = m.store.LimitRecoveries()
	if err != nil || len(states) != 0 {
		t.Fatalf("recovery did not clear: %v %v", states, err)
	}
	if recoverLimit(t, p, sess, pane, status.Errored, true, true, observed.Add(24*time.Hour)) {
		t.Fatal("new limit sent before new deadline")
	}
	states, err = m.store.LimitRecoveries()
	if err != nil || !states[sess.ID].ResetAt.Equal(deadline.Add(24*time.Hour-limitResetGrace)) {
		t.Fatalf("new limit not scheduled: %v %v", states, err)
	}
}

func TestLimitRecoveryHoldsUnsafePrompts(t *testing.T) {
	m := buildModel(t)
	configureLimitCodex(t, m)
	sess := spawnedSession(t, m, "codex")
	now := time.Date(2026, 9, 6, 21, 5, 0, 0, time.UTC)
	pane := "You've hit your usage limit. Try again at Sep 6th, 2026 9:00 PM.\n\n› "
	for _, tc := range []struct {
		name, pane, derived                  string
		alive, available, archived, operator bool
	}{
		{"dead", pane, status.Errored, false, true, false, false},
		{"archived", pane, status.Errored, true, true, true, false},
		{"working", pane, status.Working, true, true, false, false},
		{"launch input", pane, status.Errored, true, false, false, false},
		{"dialog", strings.Replace(pane, "› ", "Press enter to confirm\n› ", 1), status.Errored, true, true, false, false},
		{"scrolled", strings.Replace(pane, "› ", "Jump to bottom (ctrl+End)\n› ", 1), status.Errored, true, true, false, false},
		{"operator", pane, status.Errored, true, true, false, true},
		{"unknown", "You've hit your limit · resets tomorrow sometime\n› ", status.Errored, true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := sess
			candidate.Archived = tc.archived
			if tc.operator {
				m.poller.noteOperatorInput(sess.ID)
			}
			if recoverLimit(t, m.poller, candidate, tc.pane, tc.derived, tc.alive, tc.available, now) {
				t.Fatal("unsafe continuation sent")
			}
		})
	}
}

func TestLimitRecoveryDoesNotSubmitTypedInput(t *testing.T) {
	m := buildModel(t)
	configureLimitCodex(t, m)
	tool := m.cfg.Tools["codex"]
	tool.Command = "sh -c " + tmux.ShellQuote(`printf '%s\n' "You've hit your usage limit. Try again at Sep 6th, 2026 9:00 PM."; printf '› '; cat`+soleWriter)
	m.cfg.Tools["codex"] = tool
	engine, err := status.NewEngine(m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.poller.engine = engine
	sess := spawnedSession(t, m, "codex")
	settledPane(t, m, sess.ID, "Try again at", "›")
	if err := m.tmux.Paste(sess.ID, "USERTEXT-in-progress"); err != nil {
		t.Fatal(err)
	}
	pane := settledPane(t, m, sess.ID, "USERTEXT-in-progress")
	now := time.Date(2026, 9, 6, 21, 5, 0, 0, time.UTC)
	if recoverLimit(t, m.poller, sess, pane, status.Errored, true, true, now) {
		t.Fatal("submitted operator text")
	}
	if err := m.tmux.SendKeys(sess.ID, "C-u"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pane, err = m.tmux.CapturePane(sess.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(pane, "USERTEXT-in-progress") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !recoverLimit(t, m.poller, sess, pane, status.Errored, true, true, now) {
		t.Fatal("did not resume after typed input was cleared")
	}
}

func TestLimitRecoveryResumesManagedCodexChild(t *testing.T) {
	m := buildModel(t)
	configureLimitCodex(t, m)
	parent := spawnedSession(t, m, "claude-hooked")
	tool := m.cfg.Tools["codex"]
	child := store.Session{ID: newID(), Name: "limited-child", Tool: "codex", Cwd: t.TempDir(), ParentID: parent.ID, Status: status.Errored}
	if err := m.launchNewSession(child, tool, tool.Command); err != nil {
		t.Fatal(err)
	}
	child, err := m.store.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	pane := "■ You've hit your usage limit. Try again at Sep 6th, 2026 9:00 PM.\n\n› "
	if !recoverLimit(t, m.poller, child, pane, status.Errored, true, true, time.Date(2026, 9, 6, 21, 5, 0, 0, time.UTC)) {
		t.Fatal("managed Codex child did not resume")
	}
	settledPane(t, m, child.ID, "Continue the interrupted task")
}

func TestLimitRecoveryPollKeepsLaunchInputsAndInboxBehindRecovery(t *testing.T) {
	m := buildModel(t)
	configureLimitCodex(t, m)
	tool := m.cfg.Tools["codex"]
	tool.Command = "sh -c " + tmux.ShellQuote(`printf '%s\n' "You've hit your usage limit. Try again at Sep 5th, 2026 9:00 PM."; printf '› '; cat`+soleWriter)
	m.cfg.Tools["codex"] = tool
	sess := store.Session{ID: newID(), Name: "limited-launch", Tool: "codex", Cwd: t.TempDir(), Status: status.Errored, PendingInputs: []string{"AFTER-RECOVERY"}}
	if err := m.launchNewSession(sess, tool, tool.Command); err != nil {
		t.Fatal(err)
	}
	queueMessage(t, m, sess.ID, "QUEUED-MESSAGE")
	settledPane(t, m, sess.ID, "You've hit your usage limit")
	msg := m.poller.refreshOnce()
	if failed, ok := msg.(errMsg); ok {
		t.Fatal(failed.err)
	}
	pane := settledPane(t, m, sess.ID, "Continue the interrupted task")
	if strings.Contains(pane, "AFTER-RECOVERY") || strings.Contains(pane, "QUEUED-MESSAGE") {
		t.Fatalf("another input overtook recovery: %s", pane)
	}
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.PendingInputs) != 1 {
		t.Fatal("pending launch input was consumed")
	}
	if queued, err := m.store.QueuedCount(sess.ID); err != nil || queued != 1 {
		t.Fatalf("inbox = %d %v", queued, err)
	}
}

func TestLimitRecoveryLeavesClaudeToNativeResume(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	now := time.Date(2026, 9, 6, 21, 5, 0, 0, time.UTC)
	pane := "You've hit your session limit · resets 9pm (UTC)\n\n❯ "
	for _, tool := range []string{"claude", "claude-hooked", "other-tool"} {
		candidate := sess
		candidate.Tool = tool
		if recoverLimit(t, m.poller, candidate, pane, status.Errored, true, true, now) {
			t.Fatalf("continued %s", tool)
		}
	}
	states, err := m.store.LimitRecoveries()
	if err != nil || len(states) != 0 {
		t.Fatalf("scheduled non-Codex recovery: %v %v", states, err)
	}
	previous := store.LimitRecovery{Banner: m.poller.engine.LimitBanner(sess.Tool, pane), ResetAt: now.Add(-time.Hour), LaunchAt: sess.LaunchTime()}
	if saved, err := m.store.CompareLimitRecovery(sess, nil, &previous); err != nil || !saved {
		t.Fatalf("seed stale state: %v %v", saved, err)
	}
	if recoverLimit(t, m.poller, sess, pane, status.Errored, true, true, now) {
		t.Fatal("continued Claude from stale state")
	}
	states, err = m.store.LimitRecoveries()
	if err != nil || !states[sess.ID].AttemptedAt.IsZero() {
		t.Fatalf("claimed stale Claude state: %v %v", states, err)
	}
}
