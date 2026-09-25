package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// childRow files a session with a live pane, because a message is only
// queueable for a session the manager could actually type into. withPane false
// is how a row that has no business receiving one is set up.
func childRow(t *testing.T, h *sessionHarness, sess store.Session, withPane bool) store.Session {
	t.Helper()
	if sess.Tool == "" {
		sess.Tool = "echoer"
	}
	if sess.Group == "" {
		sess.Group = h.caller.Group
	}
	if sess.Status == "" {
		sess.Status = status.Idle
	}
	if sess.Cwd == "" {
		sess.Cwd = h.caller.Cwd
	}
	if withPane {
		if err := h.driver.Create(sess.ID, sess.Cwd, "sleep 60", nil, 80, 24); err != nil {
			t.Fatalf("create pane %s: %v", sess.ID, err)
		}
		t.Cleanup(func() { _ = h.driver.Kill(sess.ID) })
	}
	if err := h.store.CreateSession(sess); err != nil {
		t.Fatalf("create %s: %v", sess.ID, err)
	}
	return sess
}

func TestSendChildrenQueuesForEveryChildAtOnce(t *testing.T) {
	h := newSessionHarness(t)
	for _, id := range []string{"kid-aaa1", "kid-bbb2", "kid-ccc3"} {
		childRow(t, h, store.Session{ID: id, Name: id, ParentID: h.caller.ID}, true)
	}
	// Not this caller's, so not this caller's to instruct.
	stranger := childRow(t, h, store.Session{ID: "other001", Name: "another-agent"}, false)
	childRow(t, h, store.Session{ID: "theirs01", Name: "their-child", ParentID: stranger.ID}, true)

	sent, err := h.sessions.SendChildren(h.caller.ID, "the branch moved to abc-100009-sample-branch")
	if err != nil {
		t.Fatalf("SendChildren: %v", err)
	}
	if sent.Queued != 3 || sent.Skipped != 0 {
		t.Fatalf("queued=%d skipped=%d, want 3 and 0", sent.Queued, sent.Skipped)
	}
	for _, id := range []string{"kid-aaa1", "kid-bbb2", "kid-ccc3"} {
		head, found, err := h.store.HeadMessage(id)
		if err != nil {
			t.Fatalf("HeadMessage %s: %v", id, err)
		}
		if !found || !strings.Contains(head.Body, "the branch moved") {
			t.Errorf("%s did not get the message", id)
		}
	}
	if _, found, err := h.store.HeadMessage("theirs01"); err != nil {
		t.Fatalf("HeadMessage: %v", err)
	} else if found {
		t.Error("another session's child was sent to")
	}
}

// One child that cannot take it is not a reason to leave the rest
// uninstructed, so the skip is reported per child and the call succeeds.
func TestSendChildrenReportsWhatItPassedOver(t *testing.T) {
	h := newSessionHarness(t)
	childRow(t, h, store.Session{ID: "kid-live", Name: "kid-live", ParentID: h.caller.ID}, true)
	childRow(t, h, store.Session{ID: "kid-dead", Name: "kid-dead", ParentID: h.caller.ID, Status: status.Dead}, false)
	childRow(t, h, store.Session{ID: "kid-gone", Name: "kid-gone", ParentID: h.caller.ID, Archived: true}, true)

	sent, err := h.sessions.SendChildren(h.caller.ID, "stop using the legacy field")
	if err != nil {
		t.Fatalf("SendChildren: %v", err)
	}
	if sent.Queued != 1 || sent.Skipped != 2 {
		t.Fatalf("queued=%d skipped=%d, want 1 and 2", sent.Queued, sent.Skipped)
	}
	reasons := map[string]string{}
	for _, delivery := range sent.Deliveries {
		reasons[delivery.SessionID] = delivery.Skipped
	}
	if reasons["kid-dead"] != "dead" || reasons["kid-gone"] != "archived" {
		t.Errorf("skip reasons = %v, want them named per child", reasons)
	}
}

func TestSendChildrenSaysSoWithNoChildren(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	_, err := h.sessions.SendChildren(h.caller.ID, "anyone there")
	if err == nil || !strings.Contains(err.Error(), "no children") {
		t.Fatalf("SendChildren err = %v, want it to say there are none", err)
	}
}

func TestSendChildrenRefusesAnEmptyMessage(t *testing.T) {
	h := newSessionHarness(t)
	childRow(t, h, store.Session{ID: "kid-aaa1", Name: "kid-aaa1", ParentID: h.caller.ID}, true)
	if _, err := h.sessions.SendChildren(h.caller.ID, "  "); err == nil {
		t.Fatal("an empty message was accepted")
	}
}
