package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/netwatch"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// goOffline gives the poller a watcher that has seen the network fail often
// enough to call it an outage, and returns a switch that brings it back.
func goOffline(t *testing.T, m *Model) (restore func()) {
	t.Helper()
	up := false
	w := netwatch.New(func(context.Context) bool { return up })
	for i := 0; i < netwatch.FailuresToOffline; i++ {
		w.Check(context.Background(), time.Now())
	}
	if offline, _ := w.Offline(); !offline {
		t.Fatal("the watcher did not go offline")
	}
	m.poller.net = w
	return func() {
		up = true
		w.Check(context.Background(), time.Now())
	}
}

func TestAnOfflinePassHoldsTheMessageUntilTheNetworkReturns(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	id := queueMessage(t, m, sess.ID, "rebase on main")
	restore := goOffline(t, m)

	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
		t.Fatalf("offline pass: %v", err)
	}
	state, err := m.store.Message(id, "sender01")
	if err != nil {
		t.Fatal(err)
	}
	if !state.ClaimedAt.IsZero() || !state.DroppedAt.IsZero() || !state.DeliveredAt.IsZero() {
		t.Fatalf("an offline pass touched the message: claimed %v dropped %v delivered %v",
			state.ClaimedAt, state.DroppedAt, state.DeliveredAt)
	}

	restore()
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
		t.Fatalf("online pass: %v", err)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 0 {
		t.Fatal("the held message was not sent once the network returned")
	}
}

func TestAnOfflineSnippetGoesToTheOutboxAsTheOperatorsWords(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	goOffline(t, m)

	m.sendSentence(sess, "ship it", `"ship it"`, true, false)

	heads := queuedHeads(t, m)
	head, ok := heads[sess.ID]
	if !ok {
		t.Fatalf("nothing queued; error bar: %q", m.errBar.text)
	}
	if head.SenderID != store.HumanSenderID || head.Body != "ship it" {
		t.Fatalf("queued %q from %q, want the operator's line", head.Body, head.SenderID)
	}
	if capture, _ := m.tmux.CapturePane(sess.ID); strings.Contains(capture, "ship it") {
		t.Fatal("an offline snippet was typed into the pane")
	}
}

func TestHeldLimitContinuationsOnlyIncludesDueUnattemptedCurrentLaunch(t *testing.T) {
	now := time.Now()
	launch := now.Add(-time.Hour)
	for _, tc := range []struct {
		name                           string
		resetAt, attemptedAt, launchAt time.Time
		tool, state                    string
		archived, want                 bool
	}{
		{"due", now.Add(-limitResetGrace), time.Time{}, launch, "codex", status.Errored, false, true},
		{"grace", now.Add(-limitResetGrace + time.Nanosecond), time.Time{}, launch, "codex", status.Errored, false, false},
		{"attempted", now.Add(-time.Hour), now, launch, "codex", status.Errored, false, false},
		{"old launch", now.Add(-time.Hour), time.Time{}, launch.Add(-time.Hour), "codex", status.Errored, false, false},
		{"claude", now.Add(-time.Hour), time.Time{}, launch, "claude", status.Errored, false, false},
		{"working", now.Add(-time.Hour), time.Time{}, launch, "codex", status.Working, false, false},
		{"archived", now.Add(-time.Hour), time.Time{}, launch, "codex", status.Errored, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := store.Session{ID: "limited", Tool: tc.tool, Status: tc.state, CreatedAt: launch, Archived: tc.archived}
			recoveries := map[string]store.LimitRecovery{sess.ID: {ResetAt: tc.resetAt, AttemptedAt: tc.attemptedAt, LaunchAt: tc.launchAt}}
			held := heldLimitContinuations([]store.Session{sess}, recoveries, now)
			if held[sess.ID] != tc.want {
				t.Fatalf("held = %v, want %v", held[sess.ID], tc.want)
			}
		})
	}
}

func TestOfflineLimitRecoveryAppearsPendingUntilRestored(t *testing.T) {
	m := buildModel(t)
	configureLimitCodex(t, m)
	tool := m.cfg.Tools["codex"]
	pane := "You've hit your usage limit. Try again at Sep 5th, 2026 9:00 PM."
	tool.Command = "sh -c " + tmux.ShellQuote("printf '%s\n' "+tmux.ShellQuote(pane)+"; printf '› '; cat"+soleWriter)
	m.cfg.Tools["codex"] = tool
	sess := store.Session{ID: newID(), Name: "limited-offline", Tool: "codex", Cwd: t.TempDir(), Status: status.Errored}
	if err := m.launchNewSession(sess, tool, tool.Command); err != nil {
		t.Fatal(err)
	}
	settledPane(t, m, sess.ID, "You've hit your usage limit")
	restore := goOffline(t, m)
	msg := m.poller.refreshOnce()
	refresh, ok := msg.(refreshMsg)
	if !ok {
		t.Fatalf("refresh returned %T: %v", msg, msg)
	}
	found := false
	for _, shown := range refresh.sessions {
		if shown.ID == sess.ID {
			found = true
			if shown.Status != status.Pending {
				t.Fatalf("offline recovery status = %q, want pending", shown.Status)
			}
		}
	}
	if !found {
		t.Fatal("limited session absent from refresh")
	}
	states, err := m.store.LimitRecoveries()
	if err != nil {
		t.Fatal(err)
	}
	state, ok := states[sess.ID]
	if !ok || !state.AttemptedAt.IsZero() {
		t.Fatalf("offline recovery was not held unattempted: %+v", state)
	}
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status == status.Pending {
		t.Fatal("pending overlay was persisted")
	}
	restore()
	if !recoverLimit(t, m.poller, stored, pane+"\n\n› ", status.Errored, true, true, time.Now()) {
		t.Fatal("held recovery did not resume after connectivity returned")
	}
	settledPane(t, m, sess.ID, "Continue the interrupted task")
}
