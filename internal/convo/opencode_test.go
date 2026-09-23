package convo

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// opencode writes "New session - <timestamp>" as the title before its title
// model runs. It is never empty, so a naming pass that reads it literally
// compresses the timestamp into a row name the moment a session opens.
func TestOpencodePlaceholderTitleReadsAsEmpty(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"Fix the flaky exporter", "Fix the flaky exporter"},
		{"New session - 2026-09-12T04:53:58.053Z", ""},
		{"New session", ""},
		{"", ""},
		{"New sessions list view", "New sessions list view"},
	} {
		if got := deplaceholder(tc.title); got != tc.want {
			t.Errorf("deplaceholder(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

func TestOpencodeConversationsDropPlaceholderTitles(t *testing.T) {
	now := time.Now().UnixMilli()
	db := t.TempDir() + "/opencode.db"
	writeOpencodeFixture(t, db, []opencodeRow{
		{id: "real-1", dir: "/repo/a", title: "Fix the flaky exporter", updated: now},
		{id: "ph-1", dir: "/repo/b", title: "New session - 2026-09-12T04:53:58.053Z", updated: now},
		{id: "ph-2", dir: "/repo/c", title: "New session", updated: now},
		{id: "old-1", dir: "/repo/d", title: "New session - 2026-09-10T00:00:00.000Z", updated: now - 48*60*60*1000},
	})

	ix := New("", db)
	convos, err := ix.opencodeConversations(&Cost{})
	if err != nil {
		t.Fatalf("opencodeConversations: %v", err)
	}
	byID := map[string]Conversation{}
	for _, convo := range convos {
		byID[convo.ID] = convo
	}
	if got := byID["real-1"].Title; got != "Fix the flaky exporter" {
		t.Errorf("real title = %q", got)
	}
	for _, id := range []string{"ph-1", "ph-2"} {
		convo, ok := byID[id]
		if !ok {
			t.Errorf("%s has no conversation: the placeholder must still link, only its title drops", id)
			continue
		}
		if convo.Title != "" {
			t.Errorf("%s title = %q, want empty until the real one lands", id, convo.Title)
		}
	}
	if _, ok := byID["old-1"]; ok {
		t.Error("a session untouched for two days is outside the window")
	}
}

type opencodeRow struct {
	id, dir, title string
	updated        int64
}

func writeOpencodeFixture(t *testing.T, path string, rows []opencodeRow) {
	t.Helper()
	if !filepath.IsAbs(path) {
		t.Fatalf("fixture path must be absolute: %q", path)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE session_v2 (
		id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '',
		directory TEXT NOT NULL, title TEXT NOT NULL,
		parent_id TEXT, time_updated INTEGER NOT NULL, time_archived INTEGER)`); err != nil {
		t.Fatal(err)
	}
	// opencodeExcerpts reads this after the sessions; without it the
	// conversations come back with an error instead of excerpts.
	if _, err := db.Exec(`CREATE TABLE session_message (
		session_id TEXT NOT NULL, type TEXT NOT NULL DEFAULT '', data TEXT NOT NULL, time_created INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if _, err := db.Exec(`INSERT INTO session_v2 (id, directory, title, time_updated) VALUES (?, ?, ?, ?)`,
			row.id, row.dir, row.title, row.updated); err != nil {
			t.Fatal(err)
		}
	}
}
