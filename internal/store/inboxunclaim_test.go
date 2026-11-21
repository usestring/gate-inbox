package store

import (
	"testing"
	"time"
)

// A paste held off a dialog never reached the pane, so its claim is given
// back and the message is the queue's head again, unclaimed. Only the claim
// that was taken can be given back: a later claim belongs to another send.
func TestAnUnclaimedMessageIsQueuedAgain(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	id, _, err := st.Enqueue(message("rebase on main", now), DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	claimAt := now.Add(time.Second)
	if claimed, err := st.ClaimMessage(id, claimAt); err != nil || !claimed {
		t.Fatalf("ClaimMessage: %v, claimed=%v", err, claimed)
	}
	if gave, err := st.UnclaimMessage(id, claimAt.Add(time.Nanosecond)); err != nil || gave {
		t.Fatalf("a different claim was given back: %v, err %v", gave, err)
	}
	if gave, err := st.UnclaimMessage(id, claimAt); err != nil || !gave {
		t.Fatalf("UnclaimMessage: %v, err %v", gave, err)
	}
	head, ok, err := st.HeadMessage("target01")
	if err != nil || !ok || head.ID != id || !head.ClaimedAt.IsZero() {
		t.Fatalf("head = %+v ok=%v err=%v, want the message queued and unclaimed", head, ok, err)
	}
	if claimed, err := st.ClaimMessage(id, now.Add(2*time.Second)); err != nil || !claimed {
		t.Fatalf("the requeued message cannot be claimed again: %v, claimed=%v", err, claimed)
	}
}
