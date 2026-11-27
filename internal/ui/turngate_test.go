package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// A turn gate that holds keeps a queued message out of the pane and in the
// queue; once it lets the turn through, the next pass delivers it. The gate
// is asked from the board, about that session and message.
func TestATurnGateHoldsAQueuedMessageUntilItLetsGo(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "deaf-tool")
	queueMessage(t, m, sess.ID, "rebase on main")
	hold := true
	var asked []string
	var source extension.TurnSource
	m.poller.gateTurn = func(s store.Session, src extension.TurnSource, id int64) extension.TurnVerdict {
		asked, source = append(asked, s.ID), src
		if id == 0 {
			t.Error("the gate was not told the message")
		}
		return extension.TurnVerdict{Hold: hold}
	}
	if err := m.poller.maybeDeliverInbox(sess, queuedHeads(t, m), tmux.Capture{Text: "❯ "}, status.Idle, true); err != nil {
		t.Fatal(err)
	}
	if queued, err := m.store.QueuedCount(sess.ID); err != nil || queued != 1 {
		t.Fatalf("queued = %d (%v): a held message was delivered", queued, err)
	}
	if len(asked) != 1 || asked[0] != sess.ID || source != extension.TurnFromBoard {
		t.Fatalf("asked %v from %q", asked, source)
	}
	hold = false
	if err := m.poller.maybeDeliverInbox(sess, queuedHeads(t, m), tmux.Capture{Text: "❯ "}, status.Idle, true); err != nil {
		t.Fatal(err)
	}
	if !m.poller.awaitSends(10 * time.Second) {
		t.Fatal("the send never finished")
	}
	if queued, err := m.store.QueuedCount(sess.ID); err != nil || queued != 0 {
		t.Fatalf("queued = %d (%v) after the gate let go", queued, err)
	}
}
