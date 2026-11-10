package store

import (
	"testing"
	"time"
)

// LastDelivered is the newest message typed into each session, and ignores
// what is still queued: a keep-alive waiting behind the operator's own line
// has not started the turn the session is resting on.
func TestLastDeliveredIsTheNewestDeliveredMessage(t *testing.T) {
	st := boardWithQueue(t, 3, 2)
	heads, err := st.HeadMessages()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	if err := st.MarkDelivered(heads["sess00"].ID, at); err != nil {
		t.Fatal(err)
	}
	last, err := st.LastDelivered()
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 1 {
		t.Fatalf("LastDelivered = %v, want only sess00", last)
	}
	if got := last["sess00"]; got.ID != heads["sess00"].ID || got.Body != "body 0" || got.DeliveredAt.IsZero() {
		t.Fatalf("sess00's last = %+v, want the delivered first message", got)
	}
	next, _, err := st.HeadMessage("sess00")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDelivered(next.ID, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	last, err = st.LastDelivered()
	if err != nil {
		t.Fatal(err)
	}
	if got := last["sess00"]; got.ID != next.ID || got.Body != "body 1" {
		t.Fatalf("sess00's last = %+v, want the second delivery", got)
	}
}
