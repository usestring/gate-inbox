package ui

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/store"

	_ "modernc.org/sqlite"
)

func opencodeChild(t *testing.T, keep int) store.Session {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "asks", "testdata", "opencode-2.0.3-session-questions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var messages []struct {
		Type        string          `json:"type"`
		TimeCreated int64           `json:"time_created"`
		TimeUpdated int64           `json:"time_updated"`
		Data        json.RawMessage `json:"data"`
	}
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
		if _, err := db.Exec(`INSERT INTO session_message VALUES (?, 'ses_child', ?, ?, ?, ?, ?)`,
			"msg_"+string(rune('a'+i)), m.Type, i, m.TimeCreated, m.TimeUpdated, string(m.Data)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GATE_OPENCODE_DB", path)
	return store.Session{ID: "child001", Name: "tier-child", Tool: "opencode", AgentSessionID: "ses_child"}
}

func opencodePane(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", "opencode", "opencode-2.0.3-"+name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAnOpencodeQuestionIsRelayedWhole(t *testing.T) {
	sess := opencodeChild(t, 0)
	body, key, urgent := childDialogBody(sess, opencodePane(t, "q2-review-none-w50.ansi"))
	for _, want := range []string{"a dialog asking 2 questions", "Question 1 of 2 [Tier]", "Which tier?", "1. Free",
		"Question 2 of 2 [Region]", "Which region?", "2. US", "type as its own answer instead"} {
		if !strings.Contains(body, want) {
			t.Errorf("relay does not say %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "resolves this question by itself") || urgent || !strings.HasPrefix(key, "q:") {
		t.Errorf("an OpenCode question was said to expire, or sent as urgent (key %q urgent %v):\n%s", key, urgent, body)
	}
}

func TestAnOpencodeMultiSelectIsTheParentsToAnswer(t *testing.T) {
	sess := opencodeChild(t, 8)
	if _, ok := asks.Pending(childAskTarget(sess)); ok {
		t.Fatal("fixture cut leaves a question pending")
	}
	multi := []convo.AskQuestion{{Header: "Checks", Question: "Which checks should CI run?", MultiSelect: true,
		Options: []convo.AskOption{{Label: "Lint"}, {Label: "Unit tests"}, {Label: "E2E tests"}}}}
	body := childQuestionsMessageFor(sess, questionsFrom(t, "multi1-ticked-w40.txt", multi), asks.TraitsOf("opencode"), sessTime)
	for _, want := range []string{"multi-select", "[x] Lint", "[ ] Unit tests", "give every option to tick, separated by commas"} {
		if !strings.Contains(body, want) {
			t.Errorf("relay does not say %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "answer_session cannot tick") {
		t.Errorf("an OpenCode multi-select was handed to a person:\n%s", body)
	}
}

func TestAnOpencodePermissionAskIsRelayedAsAPersonsCall(t *testing.T) {
	for _, capture := range []string{"perm-bash-w40.ansi", "perm-edit-w60.ansi", "perm-webfetch-w100.ansi", "perm-extdir-w50.ansi"} {
		t.Run(capture, func(t *testing.T) {
			sess := opencodeChild(t, 2)
			body, key, _ := childDialogBody(sess, opencodePane(t, capture))
			for _, want := range []string{"has stopped on a permission prompt", "Permission required", "1) Allow once (current)",
				"3) Reject", "answer_session cannot answer a permission prompt", "Left/Right moves the selection"} {
				if !strings.Contains(body, want) {
					t.Errorf("relay does not say %q:\n%s", want, body)
				}
			}
			if !strings.HasPrefix(key, "s:") {
				t.Errorf("key %q", key)
			}
		})
	}
}

func TestThePreviewCardShowsAnOpencodeDialog(t *testing.T) {
	sess := opencodeChild(t, 0)
	m := drainFleet(t)
	m.rows[m.cursor].sess.Tool = "opencode"
	selected, _ := m.selected()
	m.preview = opencodePane(t, "q2-t2-after-first-w100.ansi")
	m.askQuestions = map[string][]convo.AskQuestion{selected.ID: asks.PendingQuestions(childAskTarget(sess))}
	card := strippedRows(m.previewQuestions(60, 40))
	for _, want := range []string{"2 questions", "1. Tier", "2. Region  ◂ on screen", "Which region?", "1. EU"} {
		if !strings.Contains(card, want) {
			t.Errorf("card does not show %q:\n%s", want, card)
		}
	}
	m.preview = opencodePane(t, "perm-webfetch-w60.ansi")
	m.askQuestions = nil
	card = strippedRows(m.previewQuestions(60, 40))
	if !strings.Contains(card, "permission prompt · a person's to answer") || !strings.Contains(card, "https://example.com") ||
		!strings.Contains(card, "▸ 1. Allow once") {
		t.Errorf("permission card:\n%s", card)
	}
}

var sessTime = time.Time{}

func questionsFrom(t *testing.T, capture string, asked []convo.AskQuestion) []dialog.Question {
	t.Helper()
	reading, ok := dialog.ReadQuestions("opencode", opencodePane(t, capture), asked)
	if !ok {
		t.Fatalf("%s is not read as a question dialog", capture)
	}
	return reading.Questions
}
