package communications

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func sample(source string) Message {
	return Message{ThreadRef: ThreadRef{Source: source, Account: "personal", ThreadID: "thread-one"}, ID: "message-one", Sender: "sender", ReplyTo: []string{"recipient"}, Direction: "incoming", Body: "Can we talk?", SentAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestReplayAndThreadIdentity(t *testing.T) {
	store := testStore(t)
	ctx := t.Context()
	messages := []Message{sample("slack"), sample("email"), sample("sms")}
	other := sample("sms")
	other.Account = "work"
	messages = append(messages, other)
	count, err := store.Import(ctx, messages)
	if err != nil || count != 4 {
		t.Fatalf("import = %d, %v", count, err)
	}
	count, err = store.Import(ctx, messages)
	if err != nil || count != 0 {
		t.Fatalf("replay = %d, %v", count, err)
	}
	reopened, err := Open(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := reopened.List(ctx)
	if err != nil || len(listed) != 4 {
		t.Fatalf("list = %v, %v", listed, err)
	}
	for _, item := range listed {
		if !item.Unread {
			t.Fatal("new conversation is already read")
		}
		conversation, err := reopened.Read(ctx, item.ID)
		if err != nil || len(conversation.Messages) != 1 {
			t.Fatalf("read = %v, %v", conversation, err)
		}
	}
}

func TestConflictingImportIsAtomic(t *testing.T) {
	store := testStore(t)
	original := sample("slack")
	if _, err := store.Import(t.Context(), []Message{original}); err != nil {
		t.Fatal(err)
	}
	newMessage := original
	newMessage.ID = "new-message"
	conflict := original
	conflict.Body = "different content"
	count, err := store.Import(t.Context(), []Message{newMessage, conflict})
	if err == nil || count != 0 {
		t.Fatalf("conflict = %d, %v", count, err)
	}
	c, err := store.Read(t.Context(), original.ThreadRef.key())
	if err != nil || len(c.Messages) != 1 || c.Messages[0].Body != original.Body {
		t.Fatalf("partial import escaped: %v, %v", c, err)
	}
}

func TestRevisionsProtectReadsAndDrafts(t *testing.T) {
	store := testStore(t)
	first := sample("sms")
	if _, err := store.Import(t.Context(), []Message{first}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Read(t.Context(), first.ThreadRef.key())
	if err != nil {
		t.Fatal(err)
	}
	draft, err := store.Draft(t.Context(), c.ID, c.Revision, "Yes, tomorrow.")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Stale || draft.Source != "sms" || draft.Account != first.Account || draft.ThreadID != first.ThreadID || draft.InReplyTo != first.ID || !slices.Equal(draft.Recipients, first.ReplyTo) {
		t.Fatalf("reply lost its destination: %+v", draft)
	}
	if err := store.MarkRead(t.Context(), c.ID, c.Revision); err != nil {
		t.Fatal(err)
	}
	read, err := store.Read(t.Context(), c.ID)
	if err != nil || read.Unread {
		t.Fatalf("mark-read = %+v, %v", read, err)
	}
	late := first
	late.ID = "late-message"
	late.SentAt = first.SentAt.Add(-time.Hour)
	if _, err := store.Import(t.Context(), []Message{late}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Read(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Unread || len(updated.Drafts) != 1 || !updated.Drafts[0].Stale {
		t.Fatalf("late arrival hid new context: %+v", updated)
	}
	if err := store.MarkRead(t.Context(), c.ID, c.Revision); err == nil {
		t.Fatal("stale read cleared new context")
	}
	if _, err := store.Draft(t.Context(), c.ID, c.Revision, "old context"); err == nil {
		t.Fatal("draft accepted stale context")
	}
}

func TestReplyUsesLatestIncomingTarget(t *testing.T) {
	store := testStore(t)
	incoming := sample("email")
	newest := incoming
	newest.ID, newest.ReplyTo, newest.SentAt = "message-two", []string{"new-recipient", "other-recipient"}, incoming.SentAt.Add(time.Minute)
	outgoing := newest
	outgoing.ID, outgoing.Direction, outgoing.ReplyTo, outgoing.SentAt = "message-three", "outgoing", nil, newest.SentAt.Add(time.Minute)
	if _, err := store.Import(t.Context(), []Message{outgoing, newest, incoming}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Read(t.Context(), incoming.ThreadRef.key())
	if err != nil {
		t.Fatal(err)
	}
	draft, err := store.Draft(t.Context(), c.ID, c.Revision, "A follow-up")
	if err != nil {
		t.Fatal(err)
	}
	if draft.InReplyTo != newest.ID || !slices.Equal(draft.Recipients, newest.ReplyTo) {
		t.Fatalf("reply target = %+v", draft)
	}
}

func TestConcurrentWritersDoNotLoseImports(t *testing.T) {
	store := testStore(t)
	group, ctx := errgroup.WithContext(t.Context())
	for i := range 24 {
		group.Go(func() error {
			independent, err := Open(store.dir)
			if err != nil {
				return err
			}
			message := sample("slack")
			message.ID = fmt.Sprintf("message-%02d", i)
			_, err = independent.Import(ctx, []Message{message})
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	c, err := store.Read(t.Context(), sample("slack").ThreadRef.key())
	if err != nil || len(c.Messages) != 24 {
		t.Fatalf("concurrent imports = %d, %v", len(c.Messages), err)
	}
	if c.Messages[0].ID != "message-00" || c.Messages[23].ID != "message-23" {
		t.Fatal("equal-time ordering is unstable")
	}
}

func TestInvalidMessagesCannotEnterStore(t *testing.T) {
	tests := map[string]func(*Message){
		"unknown source": func(m *Message) { m.Source = "other" },
		"empty account":  func(m *Message) { m.Account = "" },
		"empty thread":   func(m *Message) { m.ThreadID = "" },
		"empty id":       func(m *Message) { m.ID = "" },
		"missing sender": func(m *Message) { m.Sender = "" },
		"missing target": func(m *Message) { m.ReplyTo = nil },
		"empty target":   func(m *Message) { m.ReplyTo = []string{""} },
		"bad direction":  func(m *Message) { m.Direction = "system" },
		"missing time":   func(m *Message) { m.SentAt = time.Time{} },
		"oversized body": func(m *Message) { m.Body = strings.Repeat("x", (64<<10)+1) },
	}
	for name, alter := range tests {
		t.Run(name, func(t *testing.T) {
			store := testStore(t)
			message := sample("slack")
			alter(&message)
			if _, err := store.Import(t.Context(), []Message{message}); err == nil {
				t.Fatal("invalid message imported")
			}
			listed, err := store.List(t.Context())
			if err != nil || len(listed) != 0 {
				t.Fatalf("invalid import created state: %v, %v", listed, err)
			}
		})
	}
}

func TestUnknownStoreVersionIsPreserved(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(store.dir, "inbox-v1.json")
	original := []byte(`{"version":2,"future_field":"keep me"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Import(t.Context(), []Message{sample("slack")}); err == nil {
		t.Fatal("future state was overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original) {
		t.Fatalf("future state changed: %s, %v", data, err)
	}
}

func TestLockHonorsCancellation(t *testing.T) {
	store := testStore(t)
	unlock, err := lock(t.Context(), filepath.Join(store.dir, "inbox-v1.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := store.List(ctx); err == nil {
		t.Fatal("locked store ignored cancellation")
	}
}

func TestPrivateFiles(t *testing.T) {
	store := testStore(t)
	if _, err := store.Import(t.Context(), []Message{sample("email")}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"inbox-v1.json", "inbox-v1.lock"} {
		info, err := os.Stat(filepath.Join(store.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v", name, info.Mode())
		}
	}
}

func TestStoreLimitFailureIsAtomic(t *testing.T) {
	store := testStore(t)
	message := sample("sms")
	state := diskState{Version: 1, Seen: map[string]string{}}
	for i := range maxRecords {
		copy := message
		copy.ID = fmt.Sprint(i)
		state.Messages = append(state.Messages, copy)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.dir, "inbox-v1.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Import(t.Context(), []Message{message}); err == nil {
		t.Fatal("full store accepted another message")
	}
	after, err := os.ReadFile(filepath.Join(store.dir, "inbox-v1.json"))
	if err != nil || string(after) != string(data) {
		t.Fatal("limit failure changed stored data")
	}
}

func TestEquivalentOutgoingReplay(t *testing.T) {
	store := testStore(t)
	message := sample("email")
	message.Direction, message.ReplyTo = "outgoing", []string{}
	if _, err := store.Import(t.Context(), []Message{message}); err != nil {
		t.Fatal(err)
	}
	message.ReplyTo = nil
	message.SentAt = message.SentAt.In(time.FixedZone("offset", 3600))
	if count, err := store.Import(t.Context(), []Message{message}); count != 0 || err != nil {
		t.Fatalf("equivalent replay = %d, %v", count, err)
	}
	c, err := store.Read(t.Context(), message.ThreadRef.key())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Draft(t.Context(), c.ID, c.Revision, "reply"); err == nil {
		t.Fatal("drafted a reply without an incoming target")
	}
}
