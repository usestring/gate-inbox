package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestHookAskAnsweredPrintsTheLedgerNote(t *testing.T) {
	configDir := tmuxtest.ScratchDir(t)
	st, err := store.Open(filepath.Join(configDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordAnswer(store.DialogAnswer{TargetSession: "child001", TargetToolUseID: "toolu_1",
		QuestionHash: "h", Answer: "Yes", BySession: "parent01", Mode: store.AnswerByAgent}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	payload := `{"hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_use_id":"toolu_1"}`
	var out bytes.Buffer
	if err := RunHook(strings.NewReader(payload), &out, []string{"ask-answered"}, "child001", configDir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"classifierContext"`) || !strings.Contains(out.String(), "parent01") {
		t.Fatalf("hook printed %q, want the agent note", out.String())
	}
	for _, args := range [][]string{nil, {"unknown"}, {"ask-answered", "extra"}} {
		out.Reset()
		if err := RunHook(strings.NewReader(payload), &out, args, "child001", configDir); err != nil || out.Len() != 0 {
			t.Fatalf("RunHook(%v) = %q, %v; want silence and no error", args, out.String(), err)
		}
	}
}
