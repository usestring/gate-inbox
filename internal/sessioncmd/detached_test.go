package sessioncmd

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

// A detached spawn keeps the record of who made it and nothing else: its
// creator cannot answer its dialogs, send_children does not reach it, and
// archiving the creator leaves it running. The two still talk on purpose,
// both ways, and the creator can still read it.
func TestADetachedSessionIsNotTrackedByItsCreator(t *testing.T) {
	h := newSessionHarness(t)
	creator := childRow(t, h, store.Session{
		ID: "c1d2e3f4", Name: "high-cpu-usage", ParentID: h.caller.ID, SpawnedBy: h.caller.ID,
	}, true)
	detached := childRow(t, h, store.Session{
		ID: "c1d2e3f5", Name: "gitest-tmux-no-user-config", SpawnedBy: creator.ID,
	}, true)
	nested := childRow(t, h, store.Session{
		ID: "c1d2e3f6", Name: "nested-probe", ParentID: h.caller.ID, SpawnedBy: creator.ID,
	}, true)

	got, err := h.sessions.Get(creator.ID, detached.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SpawnedBy != creator.ID || !got.Detached || got.ParentID != "" {
		t.Fatalf("detached row reads spawned_by=%q detached=%v parent=%q, want its creator %q, true, empty",
			got.SpawnedBy, got.Detached, got.ParentID, creator.ID)
	}
	if text := FormatSession(got); !strings.Contains(text, "detached from "+creator.ID) {
		t.Fatalf("text form does not name its creator: %s", text)
	}
	if got, err := h.sessions.Get(creator.ID, nested.ID); err != nil {
		t.Fatalf("Get: %v", err)
	} else if got.Detached {
		t.Fatal("a nested child reads as detached")
	}

	runtime, err := h.sessions.open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	caller, err := runtime.caller(creator.ID)
	if err != nil {
		t.Fatalf("caller: %v", err)
	}
	_, err = runtime.child(caller, detached.ID)
	if err == nil || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("answer_session on a detached session: err = %v, want a refusal saying it is detached", err)
	}
	if _, err := runtime.child(caller, nested.ID); err != nil {
		t.Fatalf("answer_session refused a nested child to its spawner: %v", err)
	}
	runtime.store.Close()

	sent, err := h.sessions.SendChildren(creator.ID, "the branch moved")
	if err != nil {
		t.Fatalf("SendChildren: %v", err)
	}
	if len(sent.Deliveries) != 1 || sent.Deliveries[0].SessionID != nested.ID {
		t.Fatalf("send_children reached %+v, want only the nested child %q", sent.Deliveries, nested.ID)
	}

	if out, err := h.sessions.Send(creator.ID, detached.ID, "how is it going?", "", false); err != nil || out.MessageID == 0 {
		t.Fatalf("creator send_session to its detached session = %+v, %v", out, err)
	}
	if out, err := h.sessions.Send(detached.ID, creator.ID, "done, see the PR", "", false); err != nil || out.MessageID == 0 {
		t.Fatalf("detached session send_session to its creator = %+v, %v", out, err)
	}
	if _, err := h.sessions.Read(creator.ID, detached.ID, ""); err != nil {
		t.Fatalf("creator read_session on its detached session: %v", err)
	}

	if _, err := h.sessions.Archive(h.caller.ID, creator.ID, true); err != nil {
		t.Fatalf("Archive creator: %v", err)
	}
	if stored, err := h.store.Get(detached.ID); err != nil {
		t.Fatalf("Get: %v", err)
	} else if stored.Archived {
		t.Error("archiving the creator archived the detached session it made for the user")
	}
	if stored, err := h.store.Get(nested.ID); err != nil {
		t.Fatalf("Get: %v", err)
	} else if !stored.Archived {
		t.Error("archiving the creator left its nested child running")
	}
}
