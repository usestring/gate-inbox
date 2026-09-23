// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"strings"
	"testing"
)

// The send itself says the message is parked. A parent learning this only
// from message_status learns it only if it thinks to ask, and on the board
// this was measured against nothing asked: 140 of 173 parent-to-child
// messages were still waiting, 31 of 31 of them sent to a starting child.
func TestASendToARecipientOnADialogSaysSoAtSendTime(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "dialog", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, worker.ID, "Enter to confirm")

	sent, err := h.sessions.Send(h.caller.ID, worker.ID, "stand down", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.MessageID == 0 {
		t.Fatal("a held message is still queued and still has an id")
	}
	if sent.Held == "" {
		t.Fatalf("the send reported nothing about the hold: %+v", sent)
	}
	if !strings.Contains(sent.Held, worker.ID) || !strings.Contains(sent.Held, "dialog") {
		t.Fatalf("the hold does not say why or what to do: %q", sent.Held)
	}
	// And the sentence the caller reads carries it, since that is what an
	// agent front hands its model rather than the struct.
	text := FormatSendResult(sent, worker.ID)
	if !strings.Contains(text, "held") || !strings.Contains(text, "dialog") {
		t.Fatalf("the formatted send hides the hold: %q", text)
	}

	// A recipient the manager will type into is not reported as held, or
	// every send would carry a warning and none would mean anything.
	resting, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "resting-worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, resting.ID, "❯")
	restingSend, err := h.sessions.Send(h.caller.ID, resting.ID, "stand down", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if restingSend.Held != "" {
		t.Fatalf("a deliverable recipient was reported as holding the queue: %q", restingSend.Held)
	}
}

// A subject is the sender's own label, so the replacement and what it
// replaced are both reported to it: one in the send, the other to whatever
// asks after the message it retired.
func TestASubjectReplacesTheSendersEarlierMessageAndBothEndsAreTold(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "dialog", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, worker.ID, "Enter to confirm")

	first, err := h.sessions.Send(h.caller.ID, worker.ID, "work the retry path", "next-task", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	second, err := h.sessions.Send(h.caller.ID, worker.ID, "stand down, we are shipping the other fix", "next-task", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if second.Superseded != 1 {
		t.Fatalf("the second send replaced %d earlier messages, want 1", second.Superseded)
	}
	if second.QueuePosition != 1 {
		t.Fatalf("the replacement is at position %d, want the front of a queue it cleared", second.QueuePosition)
	}
	if text := FormatSendResult(second, worker.ID); !strings.Contains(text, "replacing one of yours") {
		t.Fatalf("the formatted send does not say what it replaced: %q", text)
	}

	state, err := h.sessions.MessageStatus(h.caller.ID, first.MessageID)
	if err != nil {
		t.Fatalf("MessageStatus: %v", err)
	}
	if state.State != "superseded" {
		t.Fatalf("the replaced message reads as %+v, want superseded rather than lost", state)
	}
	if !strings.Contains(state.Reason, "same subject") {
		t.Fatalf("the replacement is not explained: %+v", state)
	}
}

// Without a subject nothing is replaced: two instructions on unrelated
// matters are two instructions, and collapsing them would lose one.
func TestSendsWithoutASubjectStack(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "dialog", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, worker.ID, "Enter to confirm")

	if _, err := h.sessions.Send(h.caller.ID, worker.ID, "use the staging bucket", "", false); err != nil {
		t.Fatalf("Send: %v", err)
	}
	second, err := h.sessions.Send(h.caller.ID, worker.ID, "the deploy key rotated", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if second.Superseded != 0 || second.QueuePosition != 2 {
		t.Fatalf("an unlabelled send replaced %d and queued at %d, want it behind the first", second.Superseded, second.QueuePosition)
	}
}

func TestAnOversizedSubjectIsRefused(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	worker, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Send(h.caller.ID, worker.ID, "rebase on main", strings.Repeat("x", maxSubjectBytes+1), false); err == nil ||
		!strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("an oversized subject was answered with %v", err)
	}
}
