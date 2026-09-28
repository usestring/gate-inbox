package store

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

func queueFor(t *testing.T, st *Store, to, from, body string, at time.Time) int64 {
	t.Helper()
	id, _, err := st.Enqueue(InboxMessage{
		SessionID: to, SenderID: from, SenderName: "n-" + from,
		Body: body, Fingerprint: body, SentAt: at,
	}, DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue %q: %v", body, err)
	}
	return id
}

func noticesFor(t *testing.T, st *Store, sessionID string) []InboxMessage {
	t.Helper()
	pending := true
	msgs, err := st.Inbox(sessionID, InboxFilter{Pending: &pending, Limit: 50})
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	return msgs
}

// A recipient that ends with messages queued takes them with it, and each
// session that sent one hears once which of its messages went.
func TestAnEndedRecipientDropsItsQueueAndTellsEachSenderOnce(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"worker", "boss", "peer", "filed"} {
		if err := st.CreateSession(sample(id, "g")); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	if err := st.SetArchived("filed", true); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}
	now := time.Now()
	first := queueFor(t, st, "worker", "boss", "rebase on main", now)
	second := queueFor(t, st, "worker", "boss", "then run the suite", now)
	fromPeer := queueFor(t, st, "worker", "peer", "use the new fixture", now)
	queueFor(t, st, "worker", "filed", "one more thing", now)
	queueFor(t, st, "worker", HumanSenderID, "typed at a shell", now)
	queueFor(t, st, "worker", ExtensionSenderID("sample"), "from an extension", now)

	if err := st.UpdateStatus("worker", status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	dropped, err := st.ResolveEndedRecipient("worker", now)
	if err != nil {
		t.Fatalf("ResolveEndedRecipient: %v", err)
	}
	if len(dropped) != 6 {
		t.Fatalf("dropped %d messages, want all 6 queued: %+v", len(dropped), dropped)
	}
	for id, sender := range map[int64]string{first: "boss", second: "boss", fromPeer: "peer"} {
		msg, err := st.Message(id, sender)
		if err != nil {
			t.Fatalf("Message %d: %v", id, err)
		}
		if msg.DroppedAt.IsZero() || msg.DropReason != DropRecipientEnded {
			t.Fatalf("message %d reads %+v, want dropped because its recipient ended", id, msg)
		}
	}
	if left := noticesFor(t, st, "worker"); len(left) != 0 {
		t.Fatalf("the ended recipient still has %d queued", len(left))
	}

	boss := noticesFor(t, st, "boss")
	if len(boss) != 1 {
		t.Fatalf("boss got %d notices, want one naming both messages: %+v", len(boss), boss)
	}
	for _, id := range []int64{first, second} {
		if !strings.Contains(boss[0].Body, strconv.FormatInt(id, 10)) {
			t.Fatalf("boss's notice does not name message %d: %q", id, boss[0].Body)
		}
	}
	if boss[0].SenderID != "worker" || !strings.Contains(boss[0].Body, DropRecipientEnded) {
		t.Fatalf("boss's notice does not say which recipient ended: %+v", boss[0])
	}
	peer := noticesFor(t, st, "peer")
	if len(peer) != 1 || !strings.Contains(peer[0].Body, strconv.FormatInt(fromPeer, 10)) {
		t.Fatalf("peer's notice = %+v, want one naming message %d", peer, fromPeer)
	}
	if strings.Contains(peer[0].Body, strconv.FormatInt(first, 10)+",") {
		t.Fatalf("peer was told about boss's messages: %q", peer[0].Body)
	}
	if got := noticesFor(t, st, "filed"); len(got) != 0 {
		t.Fatalf("an archived sender was queued a notice: %+v", got)
	}

	// Resolved once: a second look finds nothing left and tells nobody again.
	again, err := st.ResolveEndedRecipient("worker", now.Add(time.Second))
	if err != nil || len(again) != 0 {
		t.Fatalf("second resolve = %v, %v; want nothing", again, err)
	}
	if got := noticesFor(t, st, "boss"); len(got) != 1 {
		t.Fatalf("boss was told twice: %+v", got)
	}
}

// A row that does not read dead keeps its queue: a restart ends the pane and
// relaunches it at once, and the relaunched agent is who the queue is for.
func TestARecipientThatIsNotDeadKeepsItsQueue(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"worker", "boss"} {
		if err := st.CreateSession(sample(id, "g")); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	queueFor(t, st, "worker", "boss", "rebase on main", time.Now())
	dropped, err := st.ResolveEndedRecipient("worker", time.Now())
	if err != nil {
		t.Fatalf("ResolveEndedRecipient: %v", err)
	}
	if len(dropped) != 0 {
		t.Fatalf("a live recipient's queue was dropped: %+v", dropped)
	}
	if left := noticesFor(t, st, "worker"); len(left) != 1 {
		t.Fatalf("worker has %d queued, want its one message still waiting", len(left))
	}
	if got := noticesFor(t, st, "boss"); len(got) != 0 {
		t.Fatalf("a sender was told about a recipient that has not ended: %+v", got)
	}
	if _, err := st.ResolveEndedRecipient("no-such-session", time.Now()); err != nil {
		t.Fatalf("a deleted recipient is not an error: %v", err)
	}
}
