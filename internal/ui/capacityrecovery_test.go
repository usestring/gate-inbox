package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestCapacityRecoveryBacksOffOnlyForNewFailuresAndExhausts(t *testing.T) {
	now := time.Now()
	sess := store.Session{CreatedAt: now}
	banner := "■ The selected model is at capacity. Please try again later."
	region := banner
	var previous store.LimitRecovery
	for i, delay := range []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 320 * time.Second, 8 * time.Minute} {
		next := nextCapacityRecovery(sess, previous, i > 0, banner, region, now)
		if next == nil || !recoveryDeadline(*next).Equal(now.Add(delay)) {
			t.Fatalf("failure %d: schedule = %+v; want retry after %v", i+1, next, delay)
		}
		if nextCapacityRecovery(sess, *next, true, banner, region, now.Add(time.Second)) != nil {
			t.Fatal("polling moved a pending retry deadline")
		}
		now = recoveryDeadline(*next)
		previous = *next
		previous.AttemptedAt = now
		previous.CapacityRetries++
		if nextCapacityRecovery(sess, previous, true, banner, region, now.Add(time.Hour)) != nil {
			t.Fatal("an unchanged failure counted as a new attempt")
		}
		region += "\n› " + capacityRetryMarker(previous) + "\n" + banner
	}
	if nextCapacityRecovery(sess, previous, true, banner, region, now.Add(24*time.Hour)) != nil {
		t.Fatal("capacity retries continued after the final eight-minute attempt")
	}
	sess.AgentLaunchedAt = now.Add(time.Second)
	next := nextCapacityRecovery(sess, previous, true, banner, region, now)
	if next == nil || next.CapacityRetries != 0 {
		t.Fatal("a manually relaunched session inherited an exhausted retry budget")
	}
	next.CapacityRetries = 1
	next.AttemptedAt = now
	if nextCapacityRecovery(sess, *next, true, banner, region, now) != nil {
		t.Fatal("a retry marker from an earlier capacity episode rearmed an unchanged failure")
	}
}

func TestCapacityRecoveryKeepsItsBudgetAcrossWorkingFramesAndManagerRestart(t *testing.T) {
	m := buildModel(t)
	configureLimitCodex(t, m)
	sess := spawnedSession(t, m, "codex")
	now := time.Now()
	failure := "■ The selected model is at capacity. Please try again later."
	pane := failure + "\n\n› "
	if recoverLimit(t, m.poller, sess, pane, status.Errored, true, true, now) {
		t.Fatal("capacity retry sent immediately")
	}
	states, err := m.store.LimitRecoveries()
	if err != nil {
		t.Fatal(err)
	}
	due := recoveryDeadline(states[sess.ID])
	if recoverLimit(t, m.poller, sess, pane, status.Errored, true, true, due.Add(-time.Nanosecond)) {
		t.Fatal("capacity retry sent before its deadline")
	}
	if recoverLimit(t, m.poller, sess, pane, status.Errored, true, false, due) {
		t.Fatal("capacity retry overtook another input")
	}
	if !recoverLimit(t, m.poller, sess, pane, status.Errored, true, true, due) {
		t.Fatal("capacity retry did not resume the interrupted task")
	}
	if !m.poller.awaitSends(5 * time.Second) {
		t.Fatal("continuation delivery did not settle")
	}
	p := newPoller(m.store, m.tmux, m.poller.engine, m.poller.hooks, nil, m.poller.statusSources, nil, nil, nil, time.Second)
	recoverLimit(t, p, sess, "• Working (2s • esc to interrupt)\n\n› ", status.Working, true, true, due)
	states, err = m.store.LimitRecoveries()
	if err != nil || states[sess.ID].CapacityRetries != 1 {
		t.Fatalf("working frame reset the persisted budget: %+v, %v", states, err)
	}
	if recoverLimit(t, p, sess, pane, status.Errored, true, true, due.Add(time.Hour)) {
		t.Fatal("a manager restart repeated an already claimed retry")
	}
	pane = "› " + capacityRetryMarker(states[sess.ID]) + " Continue.\n\n" + failure + "\n\n› "
	if recoverLimit(t, p, sess, pane, status.Errored, true, true, due) {
		t.Fatal("new capacity failure retried without its increased delay")
	}
	states, err = m.store.LimitRecoveries()
	if err != nil || !recoveryDeadline(states[sess.ID]).Equal(due.Add(20*time.Second)) {
		t.Fatalf("second failure was not scheduled from observation: %+v, %v", states, err)
	}
	recoverLimit(t, p, sess, "• Done.\n\n  02:42\n\n› ", status.Finished, true, true, due)
	states, err = m.store.LimitRecoveries()
	if err != nil || len(states) != 0 {
		t.Fatalf("successful completion did not clear capacity recovery: %+v, %v", states, err)
	}
}

func TestCapacityRecoveryDoesNotLetTheInboxBypassBackoff(t *testing.T) {
	m := buildModel(t)
	configureLimitCodex(t, m)
	sess := spawnedSession(t, m, "codex")
	sess.PendingInputs = []string{"deferred launch input"}
	queueMessage(t, m, sess.ID, "queued task")
	heads, err := m.store.HeadMessages()
	if err != nil {
		t.Fatal(err)
	}
	capture := tmux.Capture{Text: "■ The selected model is at capacity. Please try again later.\n\n› ", At: time.Now()}
	if sent, err := m.poller.maybeSendPendingInputWhenReady(sess, capture, status.Errored, true); err != nil || sent {
		t.Fatalf("pending launch input bypassed capacity backoff: %v, %v", sent, err)
	}
	if sent, err := m.poller.deliverInboxHead(sess, heads, capture, status.Errored, true); err != nil || sent {
		t.Fatalf("queued message bypassed capacity backoff: %v, %v", sent, err)
	}
	if queued, err := m.store.QueuedCount(sess.ID); err != nil || queued != 1 {
		t.Fatalf("held message was consumed: %d, %v", queued, err)
	}
}
