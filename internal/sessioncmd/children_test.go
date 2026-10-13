package sessioncmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

type childFixture struct {
	t          *testing.T
	configDir  string
	claudeHome string
	store      *store.Store
	hooks      *hooks.Manager
}

func newChildFixture(t *testing.T) *childFixture {
	t.Helper()
	f := &childFixture{t: t, configDir: t.TempDir(), claudeHome: t.TempDir()}
	st, err := store.Open(filepath.Join(f.configDir, "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	f.store = st
	f.hooks = hooks.NewManager(f.configDir)
	if err := os.MkdirAll(filepath.Join(f.configDir, "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.row(store.Session{ID: "a0a0a0a0", Name: "parent", Tool: "claude", Status: status.Working})
	return f
}

func (f *childFixture) row(sess store.Session) {
	f.t.Helper()
	if sess.Tool == "" {
		sess.Tool = "claude"
	}
	if err := f.store.CreateSession(sess); err != nil {
		f.t.Fatalf("CreateSession %s: %v", sess.ID, err)
	}
}

func (f *childFixture) child(id, state string) {
	f.t.Helper()
	f.row(store.Session{ID: id, Name: "kid-" + id, ParentID: "a0a0a0a0", SpawnedBy: "a0a0a0a0", Status: state})
}

// hookLog appends one event to a child's status log, as its hooks do.
func (f *childFixture) hookLog(id, line string) {
	f.t.Helper()
	file, err := os.OpenFile(f.hooks.StatusFile(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		f.t.Fatal(err)
	}
}

// ask saves an AskUserQuestion call the way the child's ask-pending hook does.
func (f *childFixture) ask(id, toolUseID string, questions []convo.AskQuestion) {
	f.t.Helper()
	payload, err := json.Marshal(map[string]any{
		"tool_name":   "AskUserQuestion",
		"tool_use_id": toolUseID,
		"tool_input":  map[string]any{"questions": questions},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	AskPendingHook(f.configDir, id, payload)
	if _, err := os.Stat(f.hooks.PendingAskFile(id)); err != nil {
		f.t.Fatalf("ask-pending hook saved nothing: %v", err)
	}
}

func (f *childFixture) board() ChildBoard {
	f.t.Helper()
	board, err := ChildDialogs(f.configDir, f.claudeHome, "a0a0a0a0", time.Now().Add(20*time.Minute))
	if err != nil {
		f.t.Fatalf("ChildDialogs: %v", err)
	}
	return board
}

var approval = []convo.AskQuestion{{
	Header:   "Approval",
	Question: "Push branch feat/x to origin?",
	Options: []convo.AskOption{
		{Label: "Push", Description: "git push -u origin feat/x"},
		{Label: "Hold", Description: "leave it local", Preview: "line one\nline two"},
	},
}}

func TestChildDialogsCarriesAWaitingChildsQuestionsVerbatim(t *testing.T) {
	f := newChildFixture(t)
	f.child("b1b1b1b1", status.Working)
	f.hookLog("b1b1b1b1", "working PreToolUse")
	f.hookLog("b1b1b1b1", "waiting PreToolUse")
	f.ask("b1b1b1b1", "toolu_1", approval)
	f.child("c2c2c2c2", status.Working)
	f.hookLog("c2c2c2c2", "working PostToolUse")

	board := f.board()
	if board.Waiting != 1 || len(board.Children) != 2 {
		t.Fatalf("board = %+v, want 1 of 2 waiting", board)
	}
	got := board.Children[0]
	if got.ID != "b1b1b1b1" || !got.Waiting || got.Dialog == nil {
		t.Fatalf("first child = %+v, want b1b1b1b1 waiting with its dialog", got)
	}
	if got.Dialog.ToolUseID != "toolu_1" {
		t.Errorf("tool use id = %q", got.Dialog.ToolUseID)
	}
	gotJSON, _ := json.Marshal(got.Dialog.Questions)
	wantJSON, _ := json.Marshal(approval)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("questions = %s, want verbatim %s", gotJSON, wantJSON)
	}
	if got.WaitedSeconds < 19*60 || got.WaitingSince.IsZero() {
		t.Errorf("waited %ds since %v, want about 20 minutes", got.WaitedSeconds, got.WaitingSince)
	}
	if board.Children[1].Waiting {
		t.Errorf("working child reported waiting: %+v", board.Children[1])
	}

	text := FormatChildBoard(board)
	for _, want := range []string{
		"1 of 2 children waiting on a dialog",
		"child kid-b1b1b1b1 (b1b1b1b1) has waited 20m on:",
		"header: Approval",
		"question: Push branch feat/x to origin?",
		"- Push: git push -u origin feat/x",
		"- Hold: leave it local",
		"          line two",
		"others: kid-c2c2c2c2 (c2c2c2c2) working",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

// The hook log decides over the row: it is written as the dialog goes up and
// comes down, and the row only on the board's next pass.
func TestChildDialogsTrustsTheHookLogOverTheRow(t *testing.T) {
	f := newChildFixture(t)
	f.child("d3d3d3d3", status.Waiting)
	f.hookLog("d3d3d3d3", "waiting PreToolUse")
	f.hookLog("d3d3d3d3", "working PostToolUse")
	// A question answered earlier leaves its saved call behind.
	f.ask("d3d3d3d3", "toolu_old", approval)
	f.child("e4e4e4e4", status.Working)
	f.hookLog("e4e4e4e4", "waiting Notification")
	// A CLI with no hooks has only the row.
	f.row(store.Session{ID: "f5f5f5f5", Name: "codex-kid", Tool: "codex", ParentID: "a0a0a0a0", SpawnedBy: "a0a0a0a0", Status: status.Waiting})

	// Killed under its dialog: the log still says waiting, the row knows better.
	f.child("a7a7a7a7", status.Dead)
	f.hookLog("a7a7a7a7", "waiting PreToolUse")

	waiting := map[string]ChildState{}
	for _, child := range f.board().Children {
		if child.Waiting {
			waiting[child.ID] = child
		}
	}
	if _, ok := waiting["d3d3d3d3"]; ok {
		t.Error("d3d3d3d3 reported waiting after its hook logged the answer")
	}
	if child, ok := waiting["e4e4e4e4"]; !ok || child.Dialog != nil {
		t.Errorf("permission prompt child = %+v, want waiting with no dialog", child)
	}
	if _, ok := waiting["a7a7a7a7"]; ok {
		t.Error("dead child reported waiting")
	}
	if _, ok := waiting["f5f5f5f5"]; !ok {
		t.Error("hookless child the row says is waiting was missed")
	}
}

func TestChildDialogsDropsAQuestionTheTranscriptAnswered(t *testing.T) {
	f := newChildFixture(t)
	f.row(store.Session{ID: "a6a6a6a6", Name: "kid", ParentID: "a0a0a0a0", SpawnedBy: "a0a0a0a0",
		Status: status.Waiting, AgentSessionID: "conv-1", Cwd: "/work"})
	f.hookLog("a6a6a6a6", "waiting Notification")
	f.ask("a6a6a6a6", "toolu_done", approval)
	dir := filepath.Join(f.claudeHome, "projects", "-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	result := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_done","content":"ok"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "conv-1.jsonl"), []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}
	child := f.board().Children[0]
	if !child.Waiting || child.Dialog != nil {
		t.Errorf("child = %+v, want waiting on a prompt, not on the answered question", child)
	}
}

func TestChildDialogsListsOnlyTheChildrenTheCallerTracks(t *testing.T) {
	f := newChildFixture(t)
	f.child("b7b7b7b7", status.Working)
	f.child("c8c8c8c8", status.Finished)
	if err := f.store.SetArchived("c8c8c8c8", true); err != nil {
		t.Fatal(err)
	}
	// Detached: spawned by the caller, drawn under nobody.
	f.row(store.Session{ID: "d9d9d9d9", Name: "detached", SpawnedBy: "a0a0a0a0", Status: status.Waiting})
	// A grandchild is filed under the root but tracked by its own spawner.
	f.row(store.Session{ID: "e0e0e0e0", Name: "grandkid", ParentID: "a0a0a0a0", SpawnedBy: "b7b7b7b7", Status: status.Waiting})
	f.row(store.Session{ID: "f1f1f1f1", Name: "stranger", Status: status.Working})
	f.row(store.Session{ID: "a2a2a2a2", Name: "theirs", ParentID: "f1f1f1f1", SpawnedBy: "f1f1f1f1", Status: status.Waiting})

	board := f.board()
	if len(board.Children) != 1 || board.Children[0].ID != "b7b7b7b7" || board.Waiting != 0 {
		t.Errorf("board = %+v, want only b7b7b7b7", board)
	}
}

func TestChildDialogsOrdersTheLongestWaitFirst(t *testing.T) {
	f := newChildFixture(t)
	for _, id := range []string{"b1000000", "b2000000", "b3000000"} {
		f.child(id, status.Working)
		f.hookLog(id, "waiting PreToolUse")
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(f.hooks.StatusFile("b3000000"), old, old); err != nil {
		t.Fatal(err)
	}
	got := f.board().Children
	if got[0].ID != "b3000000" {
		t.Errorf("first = %s, want the hour-old dialog b3000000", got[0].ID)
	}
}

func TestChildDialogsWithoutABoard(t *testing.T) {
	board, err := ChildDialogs(t.TempDir(), t.TempDir(), "a0a0a0a0", time.Now())
	if err != nil || len(board.Children) != 0 {
		t.Errorf("board = %+v, err = %v; want empty", board, err)
	}
	if FormatChildBoard(board) != "no children" {
		t.Errorf("text = %q", FormatChildBoard(board))
	}
	if _, err := ChildDialogs(t.TempDir(), "", "", time.Now()); err == nil {
		t.Error("no session id was accepted")
	}
}

func TestFormatWait(t *testing.T) {
	for d, want := range map[time.Duration]string{
		42 * time.Second:              "42s",
		14 * time.Minute:              "14m",
		11*time.Hour + 12*time.Minute: "11h12m",
		26*time.Hour + 3*time.Minute:  "26h03m",
	} {
		if got := FormatWait(d); got != want {
			t.Errorf("FormatWait(%v) = %q, want %q", d, got, want)
		}
	}
}
