package store

import (
	"testing"
	"time"
)

func TestInboxFiltersBySenderAndDelivery(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("p1", "")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	send := func(sender, subject, body string, at time.Time) int64 {
		t.Helper()
		id, _, err := st.Enqueue(InboxMessage{SessionID: "p1", SenderID: sender, SenderName: sender,
			Subject: subject, Body: body, Fingerprint: Fingerprint(body), SentAt: at}, DefaultInboxLimits)
		if err != nil {
			t.Fatalf("Enqueue %q: %v", body, err)
		}
		return id
	}
	delivered := send("c1", "", "done: first", now.Add(-3*time.Minute))
	if err := st.MarkDelivered(delivered, now); err != nil {
		t.Fatal(err)
	}
	send(HumanSenderID, "", "a word from the operator", now.Add(-2*time.Minute))
	send("c1", "status", "superseded before delivery", now.Add(-90*time.Second))
	send("c1", "status", "still waiting", now.Add(-time.Minute))

	all, err := st.Inbox("p1", InboxFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, msg := range all {
		bodies = append(bodies, msg.Body)
	}
	if want := []string{"still waiting", "a word from the operator", "done: first"}; !equalStrings(bodies, want) {
		t.Fatalf("Inbox = %q, want %q (newest first, the superseded one left out)", bodies, want)
	}
	if all[0].Subject != "status" || !all[0].DeliveredAt.IsZero() || all[2].DeliveredAt.IsZero() {
		t.Fatalf("fields = %+v", all)
	}

	pending := true
	got, err := st.Inbox("p1", InboxFilter{SenderID: "c1", Pending: &pending, Limit: 10})
	if err != nil || len(got) != 1 || got[0].Body != "still waiting" {
		t.Fatalf("pending from c1 = %+v, %v", got, err)
	}
	pending = false
	got, err = st.Inbox("p1", InboxFilter{SenderID: "c1", Pending: &pending, Limit: 10})
	if err != nil || len(got) != 1 || got[0].Body != "done: first" {
		t.Fatalf("delivered from c1 = %+v, %v", got, err)
	}
	if got, _ := st.Inbox("p1", InboxFilter{Limit: 1}); len(got) != 1 {
		t.Fatalf("Limit 1 returned %d", len(got))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
