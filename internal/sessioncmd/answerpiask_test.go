package sessioncmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// A pi pane showing the extension's AskUserQuestion dialog, or its Submit
// page, is handed back to the Claude question path; its permission dialog
// is not.
func TestPiDriverHandsAQuestionToTheClaudePath(t *testing.T) {
	for name, want := range map[string]bool{
		"pi-1.5.1-w80-ask-approval.ansi":          true,
		"pi-1.5.1-w80-ask-multiselect-first.ansi": true,
		"pi-1.5.1-w137-ask-tabs-first.ansi":       true,
		"pi-1.5.1-w100-permission-touch.txt":      false,
		"pi-1.5.1-w80-ask-after-submit.ansi":      false,
	} {
		raw, err := os.ReadFile(filepath.Join("..", "dialog", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if got := askShaped(string(raw)); got != want {
			t.Errorf("%s: askShaped = %v, want %v", name, got, want)
		}
		if !want {
			continue
		}
		_, err = piDriver{}.answer(nil, nil, store.Session{ID: "child001", Tool: "pi"}, string(raw),
			[]QuestionAnswer{{Answer: "x"}}, true, nil, "parent", "parent01")
		if err != errAskShaped {
			t.Errorf("%s: driver = %v, want the pane handed back", name, err)
		}
	}
}

// Once a pi dialog closes, the call it saved is removed, but only if it is
// still the call the dialog showed.
func TestAskResolvedRemovesOnlyItsOwnCall(t *testing.T) {
	dir := tmuxtest.ScratchDir(t)
	path := hooks.NewManager(dir).PendingAskFile("child001")
	save := func(id string) {
		raw, _ := json.Marshal(convo.AskCall{ToolUseID: id, Questions: []convo.AskQuestion{{Question: "Q?"}}, AskedAt: time.Now()})
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	save("call-2")
	AskResolvedHook(dir, "child001", []byte(`{"tool_use_id":"call-1"}`))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a later call's file was removed: %v", err)
	}
	AskResolvedHook(dir, "child001", []byte(`{"tool_use_id":"call-2"}`))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the closed dialog's call is still saved: %v", err)
	}
}
