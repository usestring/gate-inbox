// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package store

import (
	"errors"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"testing"
	"time"
)

func subjectMessage(body, subject string, at time.Time) InboxMessage {
	msg := message(body, at)
	msg.Subject = subject
	return msg
}

// The shape the measurement was taken from: a manager sends a redirect, then
// a second redirect, then a stand-down, and the child wakes to all three in
// order and acts on the first.
func TestALaterMessageOnOneSubjectReplacesTheOneStillQueued(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()

	first, superseded, err := st.Enqueue(subjectMessage("work the retry path", "next-task", now), DefaultInboxLimits)
	if err != nil || superseded != 0 {
		t.Fatalf("first send: superseded %d, err %v", superseded, err)
	}
	second, superseded, err := st.Enqueue(subjectMessage("stand down, we are shipping the other fix", "next-task", now.Add(time.Second)), DefaultInboxLimits)
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	if superseded != 1 {
		t.Fatalf("the second message replaced %d of the sender's own, want 1", superseded)
	}

	queued, err := st.QueuedCount("target01")
	if err != nil || queued != 1 {
		t.Fatalf("queued = %d, err %v: the replaced message is still in the way", queued, err)
	}
	head, ok, err := st.HeadMessage("target01")
	if err != nil || !ok {
		t.Fatalf("HeadMessage: %v, ok=%v", err, ok)
	}
	if head.ID != second {
		t.Fatalf("the agent reads message %d, want the later one %d", head.ID, second)
	}

	// The sender is told which message replaced it, rather than reading the
	// replacement as a drop it should send again.
	replaced, err := st.Message(first, "sender01")
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if replaced.SupersededBy != second {
		t.Fatalf("message %d says it was superseded by %d, want %d", first, replaced.SupersededBy, second)
	}
	if !replaced.DroppedAt.IsZero() {
		t.Fatal("a replaced message must not read as dropped: nothing was lost")
	}
}

// Two subjects are two conversations. A stand-down replacing an unrelated
// instruction would lose the instruction.
func TestSupersessionIsScopedToOneSubjectSenderAndRecipient(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()

	if _, _, err := st.Enqueue(subjectMessage("use the staging bucket", "which-bucket", now), DefaultInboxLimits); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	other := subjectMessage("a different sender's word on the same subject", "which-bucket", now)
	other.SenderID = "sender02"
	if _, _, err := st.Enqueue(other, DefaultInboxLimits); err != nil {
		t.Fatalf("Enqueue from a second sender: %v", err)
	}
	unlabelled := message("nothing to do with it", now)
	if _, _, err := st.Enqueue(unlabelled, DefaultInboxLimits); err != nil {
		t.Fatalf("Enqueue without a subject: %v", err)
	}

	_, superseded, err := st.Enqueue(subjectMessage("use the production bucket", "which-bucket", now.Add(time.Second)), DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if superseded != 1 {
		t.Fatalf("replaced %d messages, want only this sender's own on this subject", superseded)
	}
	queued, err := st.QueuedCount("target01")
	if err != nil || queued != 3 {
		t.Fatalf("queued = %d, err %v: want the other sender's, the unlabelled one and the replacement", queued, err)
	}
}

// A claim means the paste is on its way to the pane. Retiring one would tell
// its sender it was replaced by a message the recipient is about to read.
func TestSupersessionLeavesAClaimedMessageAlone(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()

	first, _, err := st.Enqueue(subjectMessage("start on the parser", "next-task", now), DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if claimed, err := st.ClaimMessage(first, now); err != nil || !claimed {
		t.Fatalf("ClaimMessage: %v, claimed=%v", err, claimed)
	}
	_, superseded, err := st.Enqueue(subjectMessage("start on the lexer instead", "next-task", now.Add(time.Second)), DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if superseded != 0 {
		t.Fatalf("replaced %d messages, want the one being typed in left alone", superseded)
	}
}

// The queue cap counts undelivered rows, so a correction to a recipient
// already holding twenty would be refused by the very messages it replaces.
func TestAReplacementIsNotRefusedByTheQueueItClears(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	limits := DefaultInboxLimits
	limits.RateCap = 1000

	// Nineteen unrelated messages and one on the subject the replacement
	// carries, which is the shape that matters: the correction is refused by
	// a queue it can only clear one message of.
	for i := range limits.QueueCap - 1 {
		msg := message("filler "+string(rune('a'+i)), now.Add(time.Duration(i)*time.Millisecond))
		msg.Fingerprint = textfmt.Fingerprint(msg.Body)
		if _, _, err := st.Enqueue(msg, limits); err != nil {
			t.Fatalf("filling the queue at %d: %v", i, err)
		}
	}
	if _, _, err := st.Enqueue(subjectMessage("work the retry path", "next-task", now), limits); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, _, err := st.Enqueue(message("unrelated", now), limits); !errors.Is(err, ErrInboxFull) {
		t.Fatalf("an unlabelled message into a full queue = %v, want it refused", err)
	}
	id, superseded, err := st.Enqueue(subjectMessage("stand down", "next-task", now.Add(time.Minute)), limits)
	if err != nil {
		t.Fatalf("the replacement was refused by the queue it clears: %v", err)
	}
	if superseded != 1 {
		t.Fatalf("replaced %d, want the one message on its subject", superseded)
	}
	queued, err := st.QueuedCount("target01")
	if err != nil || queued != limits.QueueCap {
		t.Fatalf("queued = %d, err %v: the replacement took the slot it cleared", queued, err)
	}
	if _, err := st.Message(id, "sender01"); err != nil {
		t.Fatalf("the replacement is not in the queue: %v", err)
	}
}

// A refused insert takes the retirement back with it, or a sender loses the
// instruction it already had queued to a send that never landed.
func TestARefusedReplacementLeavesTheEarlierMessageQueued(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()

	first, _, err := st.Enqueue(subjectMessage("hold the release", "release", now), DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// The same text inside the dedupe window, which Enqueue refuses.
	if _, _, err := st.Enqueue(subjectMessage("hold the release", "release", now.Add(time.Second)), DefaultInboxLimits); !errors.Is(err, ErrInboxDuplicate) {
		t.Fatalf("a repeated message = %v, want it refused as a duplicate", err)
	}
	head, ok, err := st.HeadMessage("target01")
	if err != nil || !ok {
		t.Fatalf("HeadMessage: %v, ok=%v", err, ok)
	}
	if head.ID != first {
		t.Fatalf("head = %d, want the original %d still queued", head.ID, first)
	}
}
