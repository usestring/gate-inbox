package store

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/priority"
)

// A migration is the same agent carrying on, so the children it spawned and
// the reports they queued for it follow it to the new row. Left behind, every
// rest and dialog went to the source an account switch had just ended, the
// new row was refused its own children's dialogs, and the sweep filed them
// away as orphans.
func TestMigrationHandsTheSourcesChildrenToTheNewRow(t *testing.T) {
	st := newTestStore(t)
	for _, sess := range []Session{sample("source", "work"), sample("other", "work")} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	child := sample("child", "")
	child.ParentID, child.Status, child.Priority = "source", "finished", priority.High
	child.LastStatusAt = time.Now().Add(-time.Hour)
	grandchild := sample("grandchild", "")
	grandchild.ParentID, grandchild.SpawnedBy = "source", "child"
	detached := sample("detached", "work")
	detached.SpawnedBy = "source"
	stranger := sample("stranger", "")
	stranger.ParentID = "other"
	for _, sess := range []Session{child, grandchild, detached, stranger} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for _, msg := range []InboxMessage{
		{SessionID: "source", SenderID: "child", Body: "child finished", Subject: ChildRestSubject("child"), SentAt: now},
		{SessionID: "source", SenderID: "other", Body: "a word for the source", SentAt: now},
	} {
		if _, _, err := st.Enqueue(msg, DefaultInboxLimits); err != nil {
			t.Fatal(err)
		}
	}

	moved := sample("moved", "work")
	moved.MigrationID = "source"
	if err := st.CreateSession(moved); err != nil {
		t.Fatal(err)
	}
	// Until the new row is confirmed up the source keeps everything.
	if sess, err := st.Get("child"); err != nil || sess.ParentID != "source" {
		t.Fatalf("child before completion = %+v, %v", sess, err)
	}
	if err := st.CompleteMigration("source", "moved"); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string][2]string{
		"child":      {"moved", "moved"},
		"grandchild": {"moved", "child"},
		"detached":   {"", "source"},
		"stranger":   {"other", "other"},
	} {
		sess, err := st.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got := [2]string{sess.ParentID, SpawnerOf(sess)}; got != want {
			t.Fatalf("%s parent, spawner = %v, want %v", id, got, want)
		}
	}
	if sess, err := st.Get("child"); err != nil || sess.Priority != priority.High {
		t.Fatalf("child priority = %+v, %v", sess, err)
	}
	if sess, err := st.Get("source"); err != nil || !sess.Archived {
		t.Fatalf("source = %+v, %v, want archived", sess, err)
	}
	if got := retirable(t, st, 10*time.Minute); got != "" {
		t.Fatalf("retirable = %q, want no child read as orphaned by the retired source", got)
	}
	pending := true
	for id, want := range map[string]string{"moved": "child", "source": "other"} {
		inbox, err := st.Inbox(id, InboxFilter{Pending: &pending, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(inbox) != 1 || inbox[0].SenderID != want {
			t.Fatalf("%s inbox = %+v, want only %s's message", id, inbox, want)
		}
	}
}

// A source that is itself a child hands the children it spawned -- filed
// under its root, owned by it -- to the new row, which stays where the
// source was: under the same root, owned by the same spawner.
func TestMigrationOfAChildKeepsItsOwnSpawnerAndHandsOverItsSpawns(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("root", "work")); err != nil {
		t.Fatal(err)
	}
	spawner := sample("spawner", "")
	spawner.ParentID = "root"
	source := sample("source", "")
	source.ParentID, source.SpawnedBy = "root", "spawner"
	spawn := sample("spawn", "")
	spawn.ParentID, spawn.SpawnedBy = "root", "source"
	for _, sess := range []Session{spawner, source, spawn} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	moved := sample("moved", "")
	moved.MigrationID, moved.SpawnedBy = "source", "spawner"
	if err := st.CreateSession(moved); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteMigration("source", "moved"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][2]string{
		"moved": {"root", "spawner"},
		"spawn": {"root", "moved"},
	} {
		sess, err := st.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got := [2]string{sess.ParentID, SpawnerOf(sess)}; got != want {
			t.Fatalf("%s parent, spawner = %v, want %v", id, got, want)
		}
	}
}

// The pull requests a session opened are its work, and the board finds them
// by the session's id: the new row carries them on, and the archived source
// keeps its own record of them.
func TestMigrationCarriesTheSourcesOpenedPRs(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("source", "work")); err != nil {
		t.Fatal(err)
	}
	urls := []string{"https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2"}
	if err := st.RecordOpenedPRs("source", urls, time.Now()); err != nil {
		t.Fatal(err)
	}
	moved := sample("moved", "work")
	moved.MigrationID = "source"
	if err := st.CreateSession(moved); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordOpenedPRs("moved", urls[1:], time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteMigration("source", "moved"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"moved", "source"} {
		got, err := st.OpenedPRs(id)
		if err != nil || len(got) != 2 || got[0] != urls[0] || got[1] != urls[1] {
			t.Fatalf("%s opened PRs = %v, %v, want %v", id, got, err, urls)
		}
	}
}
