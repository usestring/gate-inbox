package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/store"
)

// The send the child sweep used to break: a parent following up on a child
// filed away after its grace window. It was refused with the two calls to
// make first; now the send makes them.
func TestSendToAnArchivedChildRestoresRevivesAndQueues(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	child, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Archive(h.caller.ID, child.ID, true); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if h.driver.Exists(child.ID) {
		t.Fatal("archiving left the pane running")
	}

	sent, err := h.sessions.Send(h.caller.ID, child.ID, "address the review comment on your PR", "", false)
	if err != nil {
		t.Fatalf("Send to an archived child: %v", err)
	}
	if want := "restored archived child " + child.ID + " and revived it"; !strings.Contains(sent.Restored, want) {
		t.Fatalf("Restored = %q, want it to say %q", sent.Restored, want)
	}
	if text := FormatSendResult(sent, child.ID); !strings.HasPrefix(text, "restored archived child "+child.ID) ||
		!strings.Contains(text, "queued message") {
		t.Fatalf("send result = %q, want the restore and then the queue", text)
	}
	row, err := h.store.Get(child.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Archived {
		t.Fatal("the child is still archived, so nothing will poll it")
	}
	if !h.driver.Exists(child.ID) {
		t.Fatal("the child was not revived, so nothing will read the message")
	}
	head, found, err := h.store.HeadMessage(child.ID)
	if err != nil || !found || !strings.Contains(head.Body, "review comment") {
		t.Fatalf("queued head = %+v found=%v err=%v", head, found, err)
	}
}

func TestSendToADeadChildRevivesAndQueues(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	child, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Kill(h.caller.ID, child.ID, extension.KillByCLI); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	sent, err := h.sessions.Send(h.caller.ID, child.ID, "one more thing", "", false)
	if err != nil {
		t.Fatalf("Send to a dead child: %v", err)
	}
	if want := "revived child " + child.ID; sent.Restored != want+" on its conversation" {
		t.Fatalf("Restored = %q, want %q", sent.Restored, want)
	}
	if !h.driver.Exists(child.ID) {
		t.Fatal("the child was not revived")
	}
}

// A running child is sent to as it always was, with nothing to report.
func TestSendToARunningChildRestoresNothing(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	child, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Tool: "resting", Name: "worker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sent, err := h.sessions.Send(h.caller.ID, child.ID, "carry on", "", false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.Restored != "" {
		t.Fatalf("Restored = %q for a running child", sent.Restored)
	}
}

// Another session's archived row is not the sender's to bring back, so it is
// refused as before, but the refusal is the exact calls to make.
func TestSendToSomeoneElsesArchivedSessionNamesTheExactCalls(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	stranger := childRow(t, h, store.Session{ID: "other001", Name: "another-agent"}, false)
	theirs := childRow(t, h, store.Session{ID: "theirs01", Name: "their-child", ParentID: stranger.ID, Archived: true}, false)

	_, err := h.sessions.Send(h.caller.ID, theirs.ID, "hello", "", false)
	if err == nil {
		t.Fatal("a send to another session's archived child was queued")
	}
	for _, want := range []string{
		`archive_session with {"session_id": "theirs01", "archived": false}`,
		`revive_session with {"session_id": "theirs01"}`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %s", err, want)
		}
	}
	if row, err := h.store.Get(theirs.ID); err != nil || !row.Archived {
		t.Fatalf("the stranger's child was restored: %+v err=%v", row, err)
	}

	dead := childRow(t, h, store.Session{ID: "theirs02", Name: "their-dead-child", ParentID: stranger.ID}, false)
	_, err = h.sessions.Send(h.caller.ID, dead.ID, "hello", "", false)
	if err == nil || !strings.Contains(err.Error(), `revive_session with {"session_id": "theirs02"}`) {
		t.Fatalf("dead stranger error = %v, want the exact revive call", err)
	}
}

func TestCLIVocabularyNamesTheExactCalls(t *testing.T) {
	t.Parallel()
	words := CLIVocabulary()
	if got := words.restoreCall("abcd1234"); got != cliCommand+" archive --restore abcd1234" {
		t.Errorf("restore call = %q", got)
	}
	if got := words.reviveCall("abcd1234"); got != cliCommand+" revive abcd1234" {
		t.Errorf("revive call = %q", got)
	}
}
