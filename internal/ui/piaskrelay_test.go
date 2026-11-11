package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/store"
)

// A pi child's question has no transcript behind it, only the call its
// ask-pending hook saved. The notice to its parent has to relay that call,
// header and descriptions included, or the parent asks its own user under the
// wrong header and its relayed answer does not match.
func TestPiApprovalNoticeRelaysTheSavedCall(t *testing.T) {
	call := convo.AskCall{
		ToolUseID: "pi-ask-1",
		AskedAt:   time.Now(),
		Questions: []convo.AskQuestion{{
			Header:   "Approval",
			Question: "May I delete the directory /tmp/sample-9000/scratchxxxxxx/w/cache?",
			Options: []convo.AskOption{
				{Label: "Approve", Description: "Delete it"},
				{Label: "Deny", Description: "Keep it"},
			},
		}},
	}
	raw, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(t.TempDir(), "ask-pending.json")
	if err := os.WriteFile(saved, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	child := store.Session{ID: "child624", Name: "pi-asker", Tool: "pi", AgentSessionID: "pi-session", Cwd: t.TempDir()}
	pane := readDialogFixture(t, "pi-1.5.1-w80-ask-approval.ansi")
	body, _, _ := childDialogBody(child, pane, saved)
	for _, want := range []string{`"header":"Approval"`, `"description":"Delete it"`, `"description":"Keep it"`, "headed Approval"} {
		if !strings.Contains(body, want) {
			t.Errorf("the notice lacks %s:\n%s", want, body)
		}
	}
	target := childAskTarget(child)
	target.PendingAskFile = saved
	if !asksForApproval(child.Tool, pane, asks.PendingQuestions(target)) {
		t.Error("the poller does not see the pi child's question as an Approval")
	}
}
