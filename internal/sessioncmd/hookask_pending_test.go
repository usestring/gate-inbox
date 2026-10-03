package sessioncmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
)

// Claude Code 2.1.286 writes an AskUserQuestion call to its transcript only
// once it is answered. The PreToolUse hook's copy is what the relay, the
// readback and digest.questions read while the dialog stands, and it stops
// counting once the transcript records the answer.
func TestThePendingCallIsReadFromTheHookUntilItIsAnswered(t *testing.T) {
	dir := t.TempDir()
	payload := `{"tool_name":"AskUserQuestion","tool_use_id":"toolu_pending","tool_input":{"questions":[` +
		`{"header":"Deploy","question":"May I push?","multiSelect":false,"options":[` +
		`{"label":"Yes","preview":"line one\nline two"},{"label":"No"}]}]}}`
	AskPendingHook(dir, "child001", []byte(payload))
	saved := hooks.NewManager(dir).PendingAskFile("child001")
	transcript := filepath.Join(dir, "conv.jsonl")
	(&transcript2{}).user("asked to ask").write(t, transcript)

	call, ok := convo.PendingAskFile(transcript, saved)
	if !ok || call.ToolUseID != "toolu_pending" || call.Questions[0].Options[0].Preview != "line one\nline two" {
		t.Fatalf("call %+v ok %v", call, ok)
	}
	if call.AskedAt.IsZero() || time.Since(call.AskedAt) > time.Minute {
		t.Errorf("asked at %v", call.AskedAt)
	}
	(&transcript2{}).user("asked to ask").result("toolu_pending").write(t, transcript)
	if _, ok := convo.PendingAskFile(transcript, saved); ok {
		t.Fatal("an answered call still read as pending")
	}
	for _, bad := range []string{`{"tool_name":"Bash","tool_use_id":"x"}`, `not json`, `{"tool_name":"AskUserQuestion"}`} {
		AskPendingHook(dir, "child002", []byte(bad))
	}
	if _, err := os.Stat(hooks.NewManager(dir).PendingAskFile("child002")); err == nil {
		t.Fatal("a payload with no call was saved")
	}
}

type transcript2 struct{ lines []string }

func (tr *transcript2) user(text string) *transcript2 {
	tr.lines = append(tr.lines, `{"type":"user","message":{"role":"user","content":"`+text+`"}}`)
	return tr
}

func (tr *transcript2) result(id string) *transcript2 {
	tr.lines = append(tr.lines, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"`+id+`","content":"ok"}]}}`)
	return tr
}

func (tr *transcript2) write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(tr.lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
