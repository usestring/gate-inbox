package asks

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type storedMessage struct {
	Type        string          `json:"type"`
	TimeCreated int64           `json:"time_created"`
	TimeUpdated int64           `json:"time_updated"`
	Data        json.RawMessage `json:"data"`
}

// OpencodeStore writes the fixture's messages into a store with OpenCode 2's
// session_message table, keeps the first keep of them (all when keep is 0),
// and points the board at it.
func OpencodeStore(t *testing.T, fixture, session string, keep int) {
	t.Helper()
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var messages []storedMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	if keep > 0 {
		messages = messages[:keep]
	}
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE session_message (id text PRIMARY KEY, session_id text NOT NULL, type text NOT NULL,
		seq integer NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i, m := range messages {
		if _, err := db.Exec(`INSERT INTO session_message VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"msg_"+string(rune('a'+i)), session, m.Type, i, m.TimeCreated, m.TimeUpdated, string(m.Data)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GATE_OPENCODE_DB", path)
}

const sessionFixture = "testdata/opencode-2.0.3-session-questions.json"

func TestOpencodeStoreAnswersForItsSession(t *testing.T) {
	OpencodeStore(t, sessionFixture, "ses_one", 0)
	target := Target{Tool: "opencode", AgentSessionID: "ses_one"}
	if !Located(target) {
		t.Fatal("the store was not located")
	}
	call, ok := Pending(target)
	if !ok || len(call.Questions) != 2 || call.Questions[0].Header != "Tier" || call.Questions[1].Options[1].Label != "US" {
		t.Fatalf("Pending = %+v, %v; want the Tier/Region call", call, ok)
	}
	parts, _, err := OpencodeQuestions(os.Getenv("GATE_OPENCODE_DB"), "ses_one")
	if err != nil || len(parts) != 4 {
		t.Fatalf("parts = %d, %v", len(parts), err)
	}
	multi, ok := ResultOf(target, parts[1].ID)
	if !ok || multi.Outcome != Answered || multi.Answers[0].Text() != "Lint, E2E tests" || !multi.Call.Questions[0].MultiSelect {
		t.Fatalf("the multi-select = %+v, %v", multi, ok)
	}
	if dismissed, ok := ResultOf(target, parts[0].ID); !ok || dismissed.Outcome != Dismissed {
		t.Fatalf("the dismissed question = %+v, %v", dismissed, ok)
	}
	answered, err := AnsweredSince(target, parts[0].CreatedAt)
	if err != nil || len(answered) != 1 || answered[0].Answers["Which checks should CI run?"] != "Lint, E2E tests" {
		t.Fatalf("AnsweredSince = %+v, %v", answered, err)
	}
	if _, ok := Unanswered(target); ok {
		t.Fatal("a pending question read as lost")
	}
	if traits := TraitsOf("opencode"); !traits.MultiSelectAnswerable || traits.Expires != 0 {
		t.Fatalf("traits = %+v", traits)
	}
}

func TestADismissedOpencodeQuestionIsLost(t *testing.T) {
	OpencodeStore(t, sessionFixture, "ses_two", 6)
	lost, ok := Unanswered(Target{Tool: "opencode", AgentSessionID: "ses_two"})
	if !ok || lost.Outcome != Dismissed || lost.Call.Questions[0].Header != "Deploy" {
		t.Fatalf("Unanswered = %+v, %v; want the dismissed Deploy question", lost, ok)
	}
}
