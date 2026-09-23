package convo

import (
	"database/sql"
	"testing"
	"time"
)

// A v2 store keeps the same session columns under session_v2 and one
// projected message per session_message row. Naming and excerpts must read
// the SQL type column: data holds user text or assistant content[].
func TestOpencodeConversationsReadV2Store(t *testing.T) {
	now := time.Now().UnixMilli()
	path := t.TempDir() + "/opencode.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE session_v2 (
		id TEXT PRIMARY KEY, directory TEXT NOT NULL, title TEXT,
		parent_id TEXT, time_updated INTEGER NOT NULL, time_archived INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session_message (
		id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL,
		seq INTEGER NOT NULL, time_created INTEGER NOT NULL,
		time_updated INTEGER NOT NULL, data TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_v2 (id, directory, title, time_updated) VALUES ('v2-1', '/repo/v2', 'Migrate the reader', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_v2 (id, directory, title, time_updated) VALUES ('untitled', '/repo/untitled', NULL, ?)`, now+1); err != nil {
		t.Fatal(err)
	}
	messages := []struct{ kind, data string }{
		{"user", `{"text":"please migrate the reader"}`},
		{"assistant", `{"content":[{"type":"text","text":"on it"},{"type":"tool","name":"edit","state":{"status":"running","input":{}}}]}`},
		{"assistant", `{"content":[{"type":"reasoning","text":"thinking"}]}`},
		{"idle", `{"text":"hidden idle event"}`},
	}
	for i, message := range messages {
		if _, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?, 'v2-1', ?, ?, ?, ?, ?)`,
			i, message.kind, i, now, now, message.data); err != nil {
			t.Fatal(err)
		}
	}

	ix := New("", path)
	convos, err := ix.opencodeConversations(&Cost{})
	if err != nil {
		t.Fatalf("opencodeConversations: %v", err)
	}
	if len(convos) != 2 {
		t.Fatalf("want 2 conversations, got %d", len(convos))
	}
	if convos[0].ID != "untitled" || convos[0].Title != "" || convos[0].Cwd != "/repo/untitled" {
		t.Errorf("untitled conversation = %+v", convos[0])
	}
	convos = convos[1:]
	if convos[0].Title != "Migrate the reader" {
		t.Errorf("title = %q", convos[0].Title)
	}
	if len(convos[0].Excerpts) != 2 {
		t.Fatalf("unexpected excerpts: %v", convos[0].Excerpts)
	}
	joined := ""
	for _, text := range convos[0].Excerpts {
		joined += text + "\n"
	}
	for _, want := range []string{"please migrate the reader", "on it"} {
		found := false
		for _, text := range convos[0].Excerpts {
			if text == want {
				found = true
			}
		}
		if !found {
			t.Errorf("excerpts %q miss %q", joined, want)
		}
	}
}
