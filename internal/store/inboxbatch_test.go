package store

import (
	"fmt"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// boardWithQueue is a board the size of the operator's, with a message waiting
// for some of it. Thirty sessions is what the poll pass walks every two
// seconds.
func boardWithQueue(t testing.TB, sessions, queued int) *Store {
	t.Helper()
	st, err := Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	for i := 0; i < sessions; i++ {
		id := fmt.Sprintf("sess%02d", i)
		if err := st.CreateSession(Session{ID: id, Name: id, Tool: "claude", Cwd: "/tmp", Status: "idle"}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if i < queued {
			// Two apiece, so the query has to pick the older one rather
			// than just any row for the session.
			for n := 0; n < 2; n++ {
				msg := InboxMessage{
					SessionID: id, SenderID: "other", SenderName: "other",
					Body: fmt.Sprintf("body %d", n), Fingerprint: fmt.Sprintf("%s-%d", id, n),
					SentAt: time.Now().Add(time.Duration(n) * time.Second),
				}
				if _, _, err := st.Enqueue(msg, InboxLimits{QueueCap: 10, RateCap: 100, RateWindow: time.Minute, DedupeWindow: time.Minute}); err != nil {
					t.Fatalf("queue for %s: %v", id, err)
				}
			}
		}
	}
	return st
}

// The batched read has to give the same answer as the per-session one, or it
// is not a replacement for it -- including picking the oldest of several
// waiting messages, and saying nothing at all about a session with none.
func TestHeadMessagesMatchesThePerSessionRead(t *testing.T) {
	const sessions, queued = 30, 11
	st := boardWithQueue(t, sessions, queued)

	heads, err := st.HeadMessages()
	if err != nil {
		t.Fatalf("HeadMessages: %v", err)
	}
	if len(heads) != queued {
		t.Fatalf("batched read found %d queued sessions, want %d", len(heads), queued)
	}
	for i := 0; i < sessions; i++ {
		id := fmt.Sprintf("sess%02d", i)
		one, present, err := st.HeadMessage(id)
		if err != nil {
			t.Fatalf("HeadMessage %s: %v", id, err)
		}
		batched, listed := heads[id]
		if present != listed {
			t.Errorf("%s: per-session read says queued=%v, batched read says %v", id, present, listed)
			continue
		}
		if !present {
			continue
		}
		if batched.ID != one.ID || batched.Body != one.Body || batched.SenderName != one.SenderName {
			t.Errorf("%s: batched read returned message %d (%q), per-session read returned %d (%q)",
				id, batched.ID, batched.Body, one.ID, one.Body)
		}
	}
}

// A delivered message must stop appearing, or the pass would type it again.
func TestHeadMessagesSkipsWhatHasBeenDelivered(t *testing.T) {
	st := boardWithQueue(t, 3, 3)
	heads, _ := st.HeadMessages()
	first := heads["sess00"]
	if err := st.MarkDelivered(first.ID, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	heads, err := st.HeadMessages()
	if err != nil {
		t.Fatalf("HeadMessages: %v", err)
	}
	next, listed := heads["sess00"]
	if !listed {
		t.Fatal("the session's second message disappeared with the first")
	}
	if next.ID == first.ID {
		t.Errorf("a delivered message is still the head; it would be typed twice")
	}
}

// TestInboxReadCost is the before/after for the poll pass's inbox phase.
func TestInboxReadCost(t *testing.T) {
	if os.Getenv("INBOX_MEASURE") == "" {
		t.Skip("set INBOX_MEASURE=1 to measure")
	}
	const sessions, queued = 30, 5
	st := boardWithQueue(t, sessions, queued)
	ids := make([]string, sessions)
	for i := range ids {
		ids[i] = fmt.Sprintf("sess%02d", i)
	}

	for pass := 1; pass <= 5; pass++ {
		mark := time.Now()
		for _, id := range ids {
			if _, _, err := st.HeadMessage(id); err != nil {
				t.Fatal(err)
			}
		}
		perSession := time.Since(mark)

		mark = time.Now()
		heads, err := st.HeadMessages()
		if err != nil {
			t.Fatal(err)
		}
		batched := time.Since(mark)

		fmt.Printf("pass %d  %d sessions: per-session=%-10s batched=%-10s speedup=%4.1fx queries=%d->1 found=%d\n",
			pass, sessions,
			perSession.Round(time.Microsecond), batched.Round(time.Microsecond),
			float64(perSession)/float64(batched), sessions, len(heads))
	}
}
