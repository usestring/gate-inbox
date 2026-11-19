package sessioncmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestPROwnerAndFeedbackFollowBoardReplacement(t *testing.T) {
	h := newSessionHarness(t)
	useWatcher(t, &launchWatcher{})
	old := childShowing(t, h, h.caller.ID, "custody01", "worker", answerPane)
	url := "https://github.com/o/r/pull/1"
	if err := h.store.RecordOpenedPRs(old.ID, []string{"https://github.com/O/R/pull/1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.PROwner(url); err == nil {
		t.Fatal("board with no heartbeat reported ownership")
	}
	if err := h.store.SetSetting(store.PollerHeartbeatKey, strconv.FormatInt(time.Now().UnixNano(), 10)); err != nil {
		t.Fatal(err)
	}
	owner, err := h.sessions.PROwner(url)
	if err != nil || owner.Status != "owned" || owner.SessionID != old.ID || owner.URL != url {
		t.Fatalf("owner=%+v,%v", owner, err)
	}
	receipt, err := h.sessions.PRFeedback(url, "lifecycle", old.ID, "feedback waiting behind the question\n")
	if err != nil || receipt.Status != "accepted" {
		t.Fatalf("feedback=%+v,%v", receipt, err)
	}
	pane, err := h.driver.CapturePane(old.ID)
	if err != nil || strings.Contains(pane, "feedback waiting") {
		t.Fatalf("enqueue typed through question: %q,%v", pane, err)
	}
	msg, err := h.store.Message(receipt.MessageID, store.SystemSenderID)
	if err != nil || !msg.DeliveredAt.IsZero() || msg.Interrupt {
		t.Fatalf("queued message=%+v,%v", msg, err)
	}
	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{Prompt: "continue the work"}, false)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = h.sessions.PROwner("https://github.com/O/R/pull/1")
	if err != nil || owner.Status != "owned" || owner.SessionID != fresh.ID || owner.URL != url {
		t.Fatalf("successor owner=%+v,%v", owner, err)
	}
	if _, err := h.sessions.PRFeedback(url, "stale-owner", old.ID, "new feedback"); err == nil {
		t.Fatal("stale resolved owner accepted new work")
	}
	if _, err := h.sessions.Send(h.caller.ID, old.ID, "stale follow-up", "", false); err == nil {
		t.Fatal("send revived retired child")
	}
	if h.driver.Exists(old.ID) {
		t.Fatal("retired pane revived")
	}
	if err := h.store.SetSetting(store.PollerHeartbeatKey, ""); err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.sessions.PRFeedback(url, "lifecycle", old.ID, "feedback waiting behind the question\n")
	if err != nil || duplicate.Status != "duplicate" || duplicate.MessageID != receipt.MessageID || duplicate.SessionID != fresh.ID {
		t.Fatalf("lost ack after handoff/board stop=%+v,%v", duplicate, err)
	}
	counts, err := h.store.QueuedCounts()
	if err != nil || counts[fresh.ID] != 1 || counts[old.ID] != 0 {
		t.Fatalf("forwarded queue=%v,%v", counts, err)
	}
}

func TestPROwnerDistinguishesUnownedAmbiguousAndUnavailable(t *testing.T) {
	h := newSessionHarness(t)
	url := "https://github.com/o/r/pull/2"
	if err := h.store.SetSetting(store.PollerHeartbeatKey, strconv.FormatInt(time.Now().UnixNano(), 10)); err != nil {
		t.Fatal(err)
	}
	owner, err := h.sessions.PROwner(url)
	if err != nil || owner.Status != "none" {
		t.Fatalf("unrecorded=%+v,%v", owner, err)
	}
	a := childShowing(t, h, h.caller.ID, "custody02", "a", "ready\n")
	b := childShowing(t, h, h.caller.ID, "custody03", "b", "ready\n")
	for _, id := range []string{a.ID, b.ID} {
		if err := h.store.RecordOpenedPRs(id, []string{url}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	owner, err = h.sessions.PROwner(url)
	if err != nil || owner.Status != "ambiguous" || len(owner.SessionIDs) != 2 || owner.SessionID != "" {
		t.Fatalf("ambiguous=%+v,%v", owner, err)
	}
	if err := h.driver.Kill(b.ID); err != nil {
		t.Fatal(err)
	}
	owner, err = h.sessions.PROwner(url)
	if err != nil || owner.Status != "owned" || owner.SessionID != a.ID {
		t.Fatalf("pane gone=%+v,%v", owner, err)
	}
	if err := h.store.SetArchived(a.ID, true); err != nil {
		t.Fatal(err)
	}
	owner, err = h.sessions.PROwner(url)
	if err != nil || owner.Status != "none" {
		t.Fatalf("archived and dead=%+v,%v", owner, err)
	}
	if err := h.store.SetSetting(store.PollerHeartbeatKey, "bad"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.PROwner(url); err == nil {
		t.Fatal("malformed heartbeat became no owner")
	}
	for _, bad := range []string{url + "?q=1", url + "/", "https://github.com/o/r/pull/0", "https://evil.example/o/r/pull/2"} {
		if _, err := h.sessions.PROwner(bad); err == nil {
			t.Fatalf("accepted noncanonical %q", bad)
		}
	}
}

func TestPROwnerDefersWhenPaneLookupFails(t *testing.T) {
	h := newSessionHarness(t)
	child := childShowing(t, h, h.caller.ID, "custody04", "worker", "ready\n")
	url := "https://github.com/o/r/pull/1"
	if err := h.store.RecordOpenedPRs(child.ID, []string{url}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSetting(store.PollerHeartbeatKey, strconv.FormatInt(time.Now().UnixNano(), 10)); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\necho 'unavailable transport' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.sessions.newDriver = func(string) (*tmux.Driver, error) { return tmux.NewWithSocket(h.driver.SocketName()) }
	if result, err := h.sessions.PROwner(url); err == nil {
		t.Fatalf("failed pane lookup became %s", result.Status)
	}
	if result, err := h.sessions.PRFeedback(url, "failed-pane", child.ID, "feedback"); err == nil {
		t.Fatalf("failed pane lookup accepted %+v", result)
	}
	count, err := h.store.QueuedCount(child.ID)
	if err != nil || count != 0 {
		t.Fatalf("failed pane lookup queued %d,%v", count, err)
	}
}
