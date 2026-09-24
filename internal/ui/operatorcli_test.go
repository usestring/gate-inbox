package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func queueFrom(t *testing.T, m *Model, targetID, senderID, body string) int64 {
	t.Helper()
	id, _, err := m.store.Enqueue(store.InboxMessage{
		SessionID:   targetID,
		SenderID:    senderID,
		Body:        body,
		Fingerprint: body,
		SentAt:      time.Now(),
	}, store.DefaultInboxLimits)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return id
}

// A line the operator sent from a shell is reported once the board has
// typed it in, and only that line: what an agent or an extension queued is
// not a person answering.
func TestAShellSendAsTheOperatorIsReportedOnceItIsDelivered(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	queueFrom(t, m, sess.ID, "sender01", "from another agent")
	human := queueFrom(t, m, sess.ID, store.HumanSenderID, "yes, ship it")
	queueFrom(t, m, sess.ID, store.ExtensionSenderID("watcher"), "from an extension")

	for attempt := 0; attempt < 20; attempt++ {
		if queued, _ := m.store.QueuedCount(sess.ID); queued == 0 {
			break
		}
		if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
			t.Fatalf("maybeDeliverInbox: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 0 {
		t.Fatalf("%d message(s) never delivered", queued)
	}
	// Another pass over an empty queue, and a second mark of the same
	// delivery, report nothing more.
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
		t.Fatal(err)
	}
	if first, err := m.store.MarkDeliveredFirst(human, time.Now()); err != nil || first {
		t.Fatalf("MarkDeliveredFirst on a delivered message = %v, %v; want false", first, err)
	}

	got := operatorInputs(observer)
	if len(got) != 1 {
		t.Fatalf("reported %+v, want the operator's one line", got)
	}
	if got[0].SessionID != sess.ID || got[0].Via != extension.OperatorCLI ||
		got[0].Text != "yes, ship it" || got[0].Dialog || got[0].At.IsZero() || got[0].MessageID != human {
		t.Fatalf("reported %+v", got[0])
	}
}

// A line relayed as the operator's words is delivered once, unfenced, like
// their own, but its delivery is not reported: the relaying extension
// recorded it at send, and a second report would answer whatever it was
// asked since. A plain shell send beside it is still reported exactly once.
func TestARelayedOperatorLineIsDeliveredButNotReportedAgain(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	relayed := queueFrom(t, m, sess.ID, store.RelayedHumanSenderID, "use the second draft")
	queueFrom(t, m, sess.ID, store.HumanSenderID, "yes, ship it")

	for attempt := 0; attempt < 20; attempt++ {
		if queued, _ := m.store.QueuedCount(sess.ID); queued == 0 {
			break
		}
		if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
			t.Fatalf("maybeDeliverInbox: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 0 {
		t.Fatalf("%d message(s) never delivered", queued)
	}
	if first, err := m.store.MarkDeliveredFirst(relayed, time.Now()); err != nil || first {
		t.Fatalf("MarkDeliveredFirst on the relayed line = %v, %v; want it delivered once already", first, err)
	}
	got := operatorInputs(observer)
	if len(got) != 1 || got[0].Via != extension.OperatorCLI || got[0].Text != "yes, ship it" {
		t.Fatalf("reported %+v, want only the shell send, once", got)
	}
	if env := inboxEnvelope(store.InboxMessage{SenderID: store.RelayedHumanSenderID, Body: "use the second draft"}, "", true, messageContext{}); env != "use the second draft" {
		t.Fatalf("relayed line typed as %q, want the operator's words unfenced", env)
	}
}
