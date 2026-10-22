package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/netwatch"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
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
