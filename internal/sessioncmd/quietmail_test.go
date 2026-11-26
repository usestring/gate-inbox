package sessioncmd

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/store"
)

// deliverQuietly queues body from sender to recipient the way the poller
// delivers it quietly: the full text kept in the store, and only the
// pointer line typed into the prompt.
func (f *channelFixture) deliverQuietly(t *testing.T, sender, recipient, body string) (int64, string) {
	t.Helper()
	id, _, err := f.store.Enqueue(store.InboxMessage{SessionID: recipient, SenderID: sender, SenderName: sender,
		Body: body, Fingerprint: fmt.Sprint(time.Now().UnixNano()), SentAt: time.Now()}, store.DefaultInboxLimits)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.store.ClaimMessage(id, time.Now()); err != nil || !ok {
		t.Fatalf("claim: %v, %v", ok, err)
	}
	fence := "----CROSS-SESSION-MESSAGE-" + sender + "-ABCDEFGH----"
	full := fmt.Sprintf("[gate-inbox] From agent %q (session %s).\n\n%s\n%s\n%s", sender, sender, fence,
		parentseal.Neutralise(body), fence)
	if err := f.store.SetQuietText(id, full); err != nil {
		t.Fatal(err)
	}
	return id, parentseal.QuietPointer(sender, sender, id)
}

func TestPromptHookHandsOverAQuietMessage(t *testing.T) {
	f := newChannelFixture(t)
	_, prompt := f.deliverQuietly(t, "parent01", "child001", "run the census again")
	if strings.Contains(prompt, "run the census again") {
		t.Fatalf("the prompt carries the body; only the pointer belongs in the pane:\n%s", prompt)
	}
	note, _ := f.submit(t, "child001", prompt)
	if !strings.Contains(note, "it was sent by your parent") {
		t.Fatalf("the parent's quiet message lost its authority:\n%s", note)
	}
	if !strings.Contains(note, "run the census again") || !strings.Contains(note, "----CROSS-SESSION-MESSAGE-") {
		t.Fatalf("the note does not hand over the fenced text:\n%s", note)
	}
}

func TestPromptHookKeepsAQuietMessageToItsRecipient(t *testing.T) {
	f := newChannelFixture(t)
	_, prompt := f.deliverQuietly(t, "parent01", "child001", "secret plan")
	// The same pointer typed into a sibling: the text is only ever read for
	// the session it was queued for.
	note, _ := f.submit(t, "sibling1", prompt)
	if strings.Contains(note, "secret plan") {
		t.Fatalf("another session's hook handed over the text:\n%s", note)
	}
}

func TestPromptHookAddsNothingToAMessageTypedWhole(t *testing.T) {
	f := newChannelFixture(t)
	_, prompt := f.deliver(t, "parent01", "child001", "typed whole")
	note, _ := f.submit(t, "child001", prompt)
	if strings.Contains(note, "full text") {
		t.Fatalf("a message typed whole got a quiet body:\n%s", note)
	}
}

func TestPromptHookHandsOverAQuietMessageOnce(t *testing.T) {
	f := newChannelFixture(t)
	_, prompt := f.deliverQuietly(t, "sibling1", "child001", "the census finished")
	first, _ := f.submit(t, "child001", prompt)
	if !strings.Contains(first, "the census finished") || !strings.Contains(first, "not your parent session") {
		t.Fatalf("a sibling's quiet message was not handed over as a sibling's:\n%s", first)
	}
	again, _ := f.submit(t, "child001", prompt)
	if strings.Contains(again, "the census finished") || !strings.Contains(again, "already handed to you once") {
		t.Fatalf("a repeated pointer handed the text over again:\n%s", again)
	}
}

func TestPromptHookRefusesAPointerToAMessageNotSentQuietly(t *testing.T) {
	f := newChannelFixture(t)
	id, _ := f.deliver(t, "parent01", "child001", "typed whole")
	note, _ := f.submit(t, "child001", parentseal.QuietPointer("parent01", "coordinator", id))
	if strings.Contains(note, "typed whole") || !strings.Contains(note, "could not verify") {
		t.Fatalf("a pointer to a message typed whole was honoured:\n%s", note)
	}
	note, _ = f.submit(t, "child001", parentseal.QuietPointer("parent01", "coordinator", 99999))
	if !strings.Contains(note, "never delivered a message 99999") {
		t.Fatalf("a pointer to no message was not refused:\n%s", note)
	}
}
