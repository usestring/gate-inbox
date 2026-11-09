package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const custodyURL = "https://github.com/o/r/pull/1"

func custodyOwner(t *testing.T, st *Store, url, want string) {
	t.Helper()
	owners, err := st.PROwners(url, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, owner := range owners {
		ids = append(ids, owner.ID)
	}
	if strings.Join(ids, ",") != want {
		t.Fatalf("custodians = %v, want %q", ids, want)
	}
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	one, err := prOwner(tx, url, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	expected := want
	if strings.Contains(want, ",") {
		expected = ""
	}
	if one != expected {
		t.Fatalf("single custodian = %q, want %q", one, expected)
	}
}

func TestPRCustodyReplacementAndLateEvidence(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("original", "")); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordOpenedPRs("original", []string{"https://github.com/O/R/pull/1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("failed launch")
	if _, err := st.ReplaceSession(sample("failed", ""), "original", "", func() error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	custodyOwner(t, st, custodyURL, "original")
	if _, err := st.ReplaceSession(sample("successor", ""), "original", "", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	custodyOwner(t, st, custodyURL, "successor")
	// Direct rows remain readable by the preceding release after rollback.
	var count int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM session_prs WHERE session_id IN ('original','successor')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("provenance/copy count = %d, %v", count, err)
	}
	late := "https://github.com/o/r/pull/2"
	// Older indexers may have written only the retired session's direct row.
	if _, err := st.db.Exec(`INSERT INTO session_prs(session_id,url,first_seen) VALUES('original',?,?)`, late, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	urls, err := st.OpenedPRs("successor")
	if err != nil || len(urls) != 2 {
		t.Fatalf("inherited late evidence = %v, %v", urls, err)
	}
	custodyOwner(t, st, late, "successor")
	if _, err := st.db.Exec(`UPDATE sessions SET status='idle', archived=0 WHERE id='original'`); err != nil {
		t.Fatal(err)
	}
	custodyOwner(t, st, custodyURL, "successor")
	if _, err := st.ReplaceSession(sample("rogue", ""), "original", "", func() error { t.Fatal("retired source launched twice"); return nil }); !errors.Is(err, ErrAlreadyReplaced) {
		t.Fatalf("replayed replacement = %v", err)
	}
	if _, err := st.ReplaceSession(sample("next", ""), "successor", "", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	custodyOwner(t, st, late, "next")
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM session_prs WHERE session_id='next'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("transitive compatibility copies = %d, %v", count, err)
	}
	newlyIndexed := "https://github.com/o/r/pull/3"
	if err := st.RecordOpenedPRs("original", []string{newlyIndexed, newlyIndexed}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM session_prs WHERE url=?`, newlyIndexed).Scan(&count); err != nil || count != 3 {
		t.Fatalf("late transitive copies = %d, %v", count, err)
	}
	for _, retired := range []string{"original", "successor"} {
		if err := st.Delete(retired); err != nil {
			t.Fatal(err)
		}
	}
	custodyOwner(t, st, newlyIndexed, "next")
	urls, err = st.OpenedPRs("next")
	if err != nil || len(urls) != 3 {
		t.Fatalf("custody after original retention = %v, %v", urls, err)
	}
}

func TestPRCustodyFailsClosedOnBrokenLineage(t *testing.T) {
	for _, tc := range []struct {
		name, changes string
		wantError     bool
		want          string
	}{
		{"dead", `UPDATE sessions SET status='dead' WHERE id='source'`, false, ""},
		{"archived", `UPDATE sessions SET archived=1 WHERE id='source'`, false, ""},
		{"shell", `UPDATE sessions SET tool='shell' WHERE id='source'`, false, ""},
		{"missing successor", `UPDATE sessions SET replaced_by='missing' WHERE id='source'`, true, ""},
		{"cycle", `UPDATE sessions SET replaced_by=CASE id WHEN 'source' THEN 'other' ELSE 'source' END`, true, ""},
		{"retired to dead", `UPDATE sessions SET replaced_by='other' WHERE id='source'; UPDATE sessions SET status='dead' WHERE id='other'`, false, ""},
		{"legacy copied author", `INSERT INTO session_prs(session_id,url,first_seen) VALUES('other','https://github.com/o/r/pull/1',1)`, false, "other,source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			for _, id := range []string{"source", "other"} {
				if err := st.CreateSession(sample(id, "")); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.RecordOpenedPRs("source", []string{custodyURL}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := st.db.Exec(tc.changes); err != nil {
				t.Fatal(err)
			}
			owners, err := st.PROwners(custodyURL, []string{"claude"})
			if tc.wantError {
				if err == nil {
					t.Fatalf("broken lineage yielded %v", owners)
				}
				late := "https://github.com/o/r/pull/2"
				if err := st.RecordOpenedPRs("source", []string{late}, time.Now()); err != nil {
					t.Fatal(err)
				}
				if owners, err := st.PROwners(late, []string{"claude"}); err == nil {
					t.Fatalf("late evidence repaired broken lineage: %v", owners)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			custodyOwner(t, st, custodyURL, tc.want)
		})
	}
}

func TestPRCustodyMigrationReplayAndQueuedWork(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"root", "source", "moved", "other"} {
		sess := sample(id, "")
		if id == "source" {
			sess.ParentID = "root"
		}
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RecordOpenedPRs("source", []string{custodyURL}, time.Now()); err != nil {
		t.Fatal(err)
	}
	custodyOwner(t, st, custodyURL, "source")
	ids := make([]int64, 4)
	for i, body := range []string{"pending", "claimed", "delivered", "dropped"} {
		id, _, err := st.Enqueue(InboxMessage{SessionID: "source", SenderID: SystemSenderID, Body: body, Fingerprint: body, SentAt: time.Now()}, DefaultInboxLimits)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	if _, err := st.ClaimMessage(ids[1], time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDelivered(ids[2], time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDropped(ids[3], time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteMigration("source", "absent"); !errors.Is(err, ErrReplacementNotRunning) {
		t.Fatalf("missing successor = %v", err)
	}
	custodyOwner(t, st, custodyURL, "source")
	if err := st.CompleteMigration("source", "moved"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := st.CompleteMigration("source", "moved"); err != nil {
			t.Fatal(err)
		}
	}
	custodyOwner(t, st, custodyURL, "moved")
	if err := st.CompleteMigration("source", "other"); !errors.Is(err, ErrAlreadyReplaced) {
		t.Fatalf("conflicting handoff = %v", err)
	}
	var archives int
	if err := st.db.QueryRow(`SELECT done FROM child_archives WHERE parent_id='root'`).Scan(&archives); err != nil || archives != 1 {
		t.Fatalf("archive replay = %d, %v", archives, err)
	}
	for i, id := range ids {
		msg, err := st.Message(id, SystemSenderID)
		if err != nil {
			t.Fatal(err)
		}
		want := "source"
		if i == 0 {
			want = "moved"
		}
		if msg.SessionID != want {
			t.Fatalf("message %d recipient=%s want %s", id, msg.SessionID, want)
		}
	}
}

func TestPRFeedbackReceiptSurvivesRestartAndHandoffs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	if err := st.CreateSession(sample("source", "")); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordOpenedPRs("source", []string{custodyURL}, time.Now()); err != nil {
		t.Fatal(err)
	}
	live := func(Session) (bool, error) { return true, nil }
	accepted, err := st.EnqueuePRFeedback(custodyURL, "event_1", "source", "feedback\n", []string{"claude"}, live, time.Now())
	if err != nil || accepted.Status != "accepted" || accepted.MessageID <= 0 {
		t.Fatalf("acceptance=%+v,%v", accepted, err)
	}
	msg, err := st.Message(accepted.MessageID, SystemSenderID)
	if err != nil || msg.SenderID != SystemSenderID || msg.Interrupt || msg.Body != "feedback\n" || !msg.DeliveredAt.IsZero() {
		t.Fatalf("ordinary guarded queue=%+v,%v", msg, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	custodyOwner(t, st, custodyURL, "source")
	if _, err := st.ReplaceSession(sample("fresh", ""), "source", "", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(sample("moved", "")); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteMigration("fresh", "moved"); err != nil {
		t.Fatal(err)
	}
	// A lost acknowledgement is answered even if no pane/board now exists.
	duplicate, err := st.EnqueuePRFeedback(custodyURL, "event_1", "source", "feedback\n", nil, func(Session) (bool, error) { t.Fatal("retry revalidated retired owner"); return false, nil }, time.Now())
	if err != nil || duplicate.Status != "duplicate" || duplicate.MessageID != accepted.MessageID || duplicate.SessionID != "moved" || duplicate.URL != custodyURL || duplicate.EventKey != "event_1" {
		t.Fatalf("receipt after handoffs=%+v,%v", duplicate, err)
	}
	counts, err := st.QueuedCounts()
	if err != nil || !reflect.DeepEqual(counts, map[string]int{"moved": 1}) {
		t.Fatalf("exactly one enqueue=%v,%v", counts, err)
	}
	if _, err := st.EnqueuePRFeedback(custodyURL, "event_1", "moved", "feedback", []string{"claude"}, live, time.Now()); err == nil {
		t.Fatal("same key accepted different bytes")
	}
	if _, err := st.EnqueuePRFeedback("https://github.com/o/r/pull/2", "event_1", "moved", "feedback\n", []string{"claude"}, live, time.Now()); err == nil {
		t.Fatal("same key accepted different URL")
	}
	if err := st.MarkDelivered(accepted.MessageID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DELETE FROM session_inbox WHERE id=?`, accepted.MessageID); err != nil {
		t.Fatal(err)
	}
	duplicate, err = st.EnqueuePRFeedback(custodyURL, "event_1", "source", "feedback\n", nil, live, time.Now())
	if err != nil || duplicate.Status != "duplicate" {
		t.Fatalf("receipt after log pruning=%+v,%v", duplicate, err)
	}
}

func TestPRFeedbackRejectsChangedOrUnsafeOwners(t *testing.T) {
	for _, tc := range []string{"wrong owner", "ambiguous", "dead", "archived", "shell", "pane gone", "pane error", "bad key", "too large", "invalid UTF8"} {
		t.Run(tc, func(t *testing.T) {
			st := newTestStore(t)
			for _, id := range []string{"source", "other"} {
				if err := st.CreateSession(sample(id, "")); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.RecordOpenedPRs("source", []string{custodyURL}, time.Now()); err != nil {
				t.Fatal(err)
			}
			owner, key, body := "source", "event", "feedback"
			live := func(Session) (bool, error) { return true, nil }
			switch tc {
			case "wrong owner":
				owner = "other"
			case "ambiguous":
				if err := st.RecordOpenedPRs("other", []string{custodyURL}, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "dead", "archived", "shell":
				changes := map[string]string{"dead": "status='dead'", "archived": "archived=1", "shell": "tool='shell'"}
				if _, err := st.db.Exec("UPDATE sessions SET " + changes[tc] + " WHERE id='source'"); err != nil {
					t.Fatal(err)
				}
			case "pane gone":
				live = func(Session) (bool, error) { return false, nil }
			case "pane error":
				live = func(Session) (bool, error) { return false, errors.New("pane check failed") }
			case "bad key":
				key = "a/b"
			case "too large":
				body = strings.Repeat("x", 8001)
			case "invalid UTF8":
				body = string([]byte{255})
			}
			if got, err := st.EnqueuePRFeedback(custodyURL, key, owner, body, []string{"claude"}, live, time.Now()); err == nil {
				t.Fatalf("unsafe acceptance=%+v", got)
			}
			var receipts, messages int
			if err := st.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key LIKE 'pr-delivery:%'`).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if err := st.db.QueryRow(`SELECT COUNT(*) FROM session_inbox`).Scan(&messages); err != nil {
				t.Fatal(err)
			}
			if receipts != 0 || messages != 0 {
				t.Fatalf("failed send left receipt=%d/messages=%d", receipts, messages)
			}
		})
	}
}

func TestPRFeedbackConcurrentRetriesAcceptOnce(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("source", "")); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordOpenedPRs("source", []string{custodyURL}, time.Now()); err != nil {
		t.Fatal(err)
	}
	other, err := Open(st.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	type outcome struct {
		receipt PRDelivery
		err     error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, db := range []*Store{st, other} {
		workers.Go(func() {
			<-start
			r, e := db.EnqueuePRFeedback(custodyURL, "concurrent", "source", "feedback", []string{"claude"}, func(Session) (bool, error) { return true, nil }, time.Now())
			results <- outcome{r, e}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	statuses := map[string]int{}
	var id int64
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		statuses[got.receipt.Status]++
		if id != 0 && id != got.receipt.MessageID {
			t.Fatal("retries accepted different messages")
		}
		id = got.receipt.MessageID
	}
	if statuses["accepted"] != 1 || statuses["duplicate"] != 1 {
		t.Fatalf("retry statuses=%v", statuses)
	}
	count, err := st.QueuedCount("source")
	if err != nil || count != 1 {
		t.Fatalf("queued=%d,%v", count, err)
	}
}
