package sessioncmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

const claudeConversation = "5f0c1e2a-0b7d-4c3e-9a51-2d8f6e4b7a10"

func (f *childFixture) nudge(event, ledger, segDir string) string {
	f.t.Helper()
	payload, _ := json.Marshal(map[string]any{"session_id": claudeConversation, "hook_event_name": event})
	return ChildNudgeHook(f.configDir, f.claudeHome, "a0a0a0a0", ledger, segDir, payload, time.Now().Add(14*time.Minute))
}

func TestChildNudgeRaisesEachDialogOnceATurn(t *testing.T) {
	f := newChildFixture(t)
	ledger := filepath.Join(t.TempDir(), "a0a0a0a0.status"+ChildNudgeSuffix)
	segDir := t.TempDir()
	f.child("b1b1b1b1", status.Working)
	f.hookLog("b1b1b1b1", "waiting PreToolUse")
	f.ask("b1b1b1b1", "toolu_1", approval)

	// The turn opens: the dialog goes into the prompt's context.
	var opened struct {
		HookSpecificOutput struct{ HookEventName, AdditionalContext string }
	}
	if err := json.Unmarshal([]byte(f.nudge("UserPromptSubmit", ledger, segDir)), &opened); err != nil {
		t.Fatalf("UserPromptSubmit printed no JSON: %v", err)
	}
	for _, want := range []string{
		"Child kid-b1b1b1b1 (b1b1b1b1) has waited 14m on: Approval — Push branch feat/x to origin?",
		"  - Push: git push -u origin feat/x",
		"marked as your user's own answer",
	} {
		if !strings.Contains(opened.HookSpecificOutput.AdditionalContext, want) {
			t.Errorf("context lacks %q:\n%s", want, opened.HookSpecificOutput.AdditionalContext)
		}
	}
	// The turn was told, so its stop is not held for the same dialog.
	if out := f.nudge("Stop", ledger, segDir); out != "" {
		t.Errorf("Stop re-raised a dialog the turn was told about: %s", out)
	}

	// A dialog that goes up mid-turn holds the stop once, and only once.
	f.child("c2c2c2c2", status.Working)
	f.hookLog("c2c2c2c2", "waiting Notification")
	var held struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(f.nudge("Stop", ledger, segDir)), &held); err != nil || held.Decision != "block" {
		t.Fatalf("Stop = %+v (%v), want a block", held, err)
	}
	if !strings.Contains(held.Reason, "kid-c2c2c2c2 (c2c2c2c2)") || !strings.Contains(held.Reason, "permission prompt") ||
		strings.Contains(held.Reason, "b1b1b1b1") {
		t.Errorf("reason names the wrong dialogs:\n%s", held.Reason)
	}
	if out := f.nudge("Stop", ledger, segDir); out != "" {
		t.Errorf("second Stop held the turn again: %s", out)
	}

	// The next turn raises both again, since both still stand.
	if out := f.nudge("UserPromptSubmit", ledger, segDir); !strings.Contains(out, "b1b1b1b1") || !strings.Contains(out, "c2c2c2c2") {
		t.Errorf("next turn's context = %s, want both dialogs", out)
	}

	seg, err := os.ReadFile(filepath.Join(segDir, claudeConversation, "gate-inbox.seg"))
	if err != nil || strings.TrimSpace(string(seg)) != "⚑2 dialogs 14m" {
		t.Errorf("segment = %q (%v)", seg, err)
	}

	// Once nothing waits the segment goes, and nothing is raised.
	f.hookLog("b1b1b1b1", "working PostToolUse")
	f.hookLog("c2c2c2c2", "working PostToolUse")
	if out := f.nudge("Stop", ledger, segDir); out != "" {
		t.Errorf("Stop with nothing waiting printed %s", out)
	}
	if _, err := os.Stat(filepath.Join(segDir, claudeConversation, "gate-inbox.seg")); !os.IsNotExist(err) {
		t.Errorf("segment left behind with nothing waiting: %v", err)
	}
}

func TestChildNudgePostToolUseOnlyRefreshesTheSegment(t *testing.T) {
	f := newChildFixture(t)
	segDir := t.TempDir()
	f.child("b1b1b1b1", status.Working)
	f.hookLog("b1b1b1b1", "waiting PreToolUse")
	if out := f.nudge("PostToolUse", filepath.Join(t.TempDir(), "ledger"), segDir); out != "" {
		t.Errorf("PostToolUse printed %s", out)
	}
	seg, err := os.ReadFile(filepath.Join(segDir, claudeConversation, "gate-inbox.seg"))
	if err != nil || strings.TrimSpace(string(seg)) != "⚑1 dialog 14m" {
		t.Errorf("segment = %q (%v)", seg, err)
	}
}

func TestChildNudgeIgnoresBadInput(t *testing.T) {
	f := newChildFixture(t)
	f.child("b1b1b1b1", status.Waiting)
	segDir := t.TempDir()
	if out := ChildNudgeHook(f.configDir, f.claudeHome, "a0a0a0a0", "", segDir, []byte("not json"), time.Now()); out != "" {
		t.Errorf("bad payload printed %s", out)
	}
	payload := []byte(`{"session_id":"../../escape","hook_event_name":"Stop"}`)
	ChildNudgeHook(f.configDir, f.claudeHome, "a0a0a0a0", "", segDir, payload, time.Now())
	if entries, _ := os.ReadDir(segDir); len(entries) != 0 {
		t.Errorf("a path-like session id wrote %v", entries)
	}
}

func TestChildSegmentFitsTheStatusLine(t *testing.T) {
	board := ChildBoard{Waiting: 12, Children: []ChildState{{Waiting: true, WaitedSeconds: 99 * 3600}}}
	if got := ChildSegment(board); len([]rune(got)) > segmentWidth || !strings.HasPrefix(got, "⚑12 dialogs") {
		t.Errorf("segment = %q", got)
	}
	if got := ChildSegment(ChildBoard{}); got != "" {
		t.Errorf("empty board segment = %q", got)
	}
}
