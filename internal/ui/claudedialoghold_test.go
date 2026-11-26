package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/namesweep"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// Claude Code's question dialog under its own viewport, scrolled up: the
// legend is off the screen and the selected option's marker reads as the
// input line, so to the rules the pane is a resting prompt.
const claudeScrolledQuestionPane = "❯ ship the fix\n" +
	"⏺ Two questions before I go on.\n" +
	"☐ Tests  ☐ Docs\n" +
	"Which tests should the change add?\n" +
	"❯ 1. Unit tests only\n" +
	"  2. Unit and end-to-end\n" +
	"  3. Type something.\n" +
	"                                        Jump to bottom (ctrl+End)\n"

// The records a Claude Code transcript holds around a question dialog.
const (
	claudePromptRecord = `{"type":"user","message":{"role":"user","content":"ship the fix"}}`
	claudeAskRecord    = `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"AskUserQuestion","input":{}}]}}`
	// Escape on the dialog writes these two and fires no hook.
	claudeRejectedRecord    = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"The user doesn't want to proceed with this tool use."}]}}`
	claudeInterruptedRecord = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
)

// The status log a question dialog leaves: its PreToolUse and the
// Notification Claude Code fires once the dialog is drawn.
const claudeAskStatus = "working UserPromptSubmit\nwaiting PreToolUse\nwaiting Notification\n"

// claudeConversation binds sess to a conversation whose transcript holds
// records, under a Claude Code home of the test's own, and returns the
// session and the transcript's path.
func claudeConversation(t *testing.T, m *Model, sess store.Session, records ...string) (store.Session, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	sess.AgentSessionID = "5f0c2a9e-1b7d-4e3a-9c61-2d8e4f7a0b13"
	if err := m.store.SetAgentSessionID(sess.ID, sess.AgentSessionID); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "projects", "-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sess.AgentSessionID+".jsonl")
	appendRecords(t, path, records...)
	return sess, path
}

func appendRecords(t *testing.T, path string, records ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, record := range records {
		if _, err := f.WriteString(record + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// writeDialogStatus writes the status log as the hooks left it when the
// dialog went up a minute ago, so anything the transcript records after
// this reads as later.
func writeDialogStatus(t *testing.T, m *Model, id, log string) {
	t.Helper()
	writeHookStatus(t, m, id, log)
	at := time.Now().Add(-time.Minute)
	if err := os.Chtimes(m.hooks.StatusFile(id), at, at); err != nil {
		t.Fatal(err)
	}
}

// A Claude Code session whose hooks say a dialog is up takes no message,
// whatever its pane shows: a dialog the rules cannot read takes the paste as
// its free-text answer and the Enter as the choice. Once the hooks log the
// answer, the same message goes in.
func TestInboxHoldsAMessageWhileClaudeCodeHooksReportADialog(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	sess, _ = claudeConversation(t, m, sess, claudePromptRecord, claudeAskRecord)
	queueMessage(t, m, sess.ID, "the child finished its turn")

	if hold := m.poller.engine.DeliveryHold(sess.Tool, claudeScrolledQuestionPane); hold != "" {
		t.Fatalf("fixture no longer reproduces the hazard: DeliveryHold = %q", hold)
	}
	writeDialogStatus(t, m, sess.ID, claudeAskStatus)
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), claudeScrolledQuestionPane, status.Waiting, true); err != nil {
		t.Fatalf("maybeDeliverInbox: %v", err)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 1 {
		t.Fatal("a message was typed into a dialog Claude Code's hooks reported open")
	}
	if pane, _ := m.tmux.CapturePane(sess.ID); strings.Contains(pane, "finished its turn") {
		t.Fatalf("message reached the pane:\n%s", pane)
	}
	// The operator's own send reads the same hold.
	if hold, _ := m.dialogHold(sess); !strings.Contains(hold, "dialog open") {
		t.Fatalf("dialogHold = %q, want the open-dialog hold", hold)
	}

	writeHookStatus(t, m, sess.ID, claudeAskStatus+"working PostToolUse\n")
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Working, true); err != nil {
		t.Fatalf("maybeDeliverInbox after the answer: %v", err)
	}
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
		t.Fatalf("maybeDeliverInbox at rest: %v", err)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 0 {
		t.Fatal("the message stayed held after the hooks logged the answer")
	}
}

// Escape closes the dialog without a hook, so the status log goes on saying
// waiting. The transcript records the interrupted turn, which releases the
// message: a hold on the log alone would keep it until the next prompt.
func TestInboxDeliversOnceAClaudeCodeDialogIsEscaped(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	sess, transcript := claudeConversation(t, m, sess, claudePromptRecord, claudeAskRecord)
	queueMessage(t, m, sess.ID, "the child finished its turn")
	writeDialogStatus(t, m, sess.ID, claudeAskStatus)
	appendRecords(t, transcript, claudeRejectedRecord, claudeInterruptedRecord)

	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Waiting, true); err != nil {
		t.Fatalf("maybeDeliverInbox: %v", err)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 0 {
		t.Fatal("a message stayed held after the dialog was escaped")
	}
}

// An interrupt's Escape would cancel the dialog, so it waits for the answer
// like the message does.
func TestInboxInterruptHoldsWhileClaudeCodeHooksReportADialog(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-typeahead")
	sess, _ = claudeConversation(t, m, sess, claudePromptRecord, claudeAskRecord)
	id := queueInterrupt(t, m, sess.ID, "stop querying GCP, use Axiom")
	if err := m.tmux.SendKeys(sess.ID, "❯ "); err != nil {
		t.Fatal(err)
	}
	settledPane(t, m, sess.ID, "❯")

	writeDialogStatus(t, m, sess.ID, claudeAskStatus)
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), workingPane, status.Working, true); err != nil {
		t.Fatalf("maybeDeliverInbox: %v", err)
	}
	if interrupted(m, id) {
		t.Fatal("interrupt sent keys while Claude Code's hooks reported a dialog")
	}

	writeHookStatus(t, m, sess.ID, claudeAskStatus+"working PostToolUse\n")
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), workingPane, status.Working, true); err != nil {
		t.Fatalf("maybeDeliverInbox after the answer: %v", err)
	}
	if !interrupted(m, id) {
		t.Fatal("interrupt stayed held after the dialog was answered")
	}
}

// The name sweep types into a pane only once its gate reads idle, and a
// dialog the hooks report is not idle however the pane reads.
func TestNameSweepSkipsAPaneWhoseHooksReportADialog(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	sess, _ = claudeConversation(t, m, sess, claudePromptRecord, claudeAskRecord)
	if err := m.tmux.SendKeys(sess.ID, "❯ "); err != nil {
		t.Fatal(err)
	}
	settledPane(t, m, sess.ID, "❯")
	gate := m.nameSweepGate()
	target := namesweep.Verdict{Candidate: namesweep.Candidate{ID: sess.ID, Tool: sess.Tool, Cwd: sess.Cwd, AgentSessionID: sess.AgentSessionID}}
	if state, err := gate.Status(target); err != nil || state != status.Idle {
		t.Fatalf("fixture no longer reads as a resting prompt: Status = %q, %v", state, err)
	}
	writeDialogStatus(t, m, sess.ID, claudeAskStatus)
	if state, err := gate.Status(target); err != nil || state != status.Waiting {
		t.Fatalf("Status = %q, %v; want %q", state, err, status.Waiting)
	}
}

// A pass that found no dialog pastes, and the agent can put one up before
// the paste's Enter: the hooks are read again then, and the Enter is held.
func TestASendHoldsItsEnterWhenTheHooksReportADialog(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "claude-hooked")
	sess, _ = claudeConversation(t, m, sess, claudePromptRecord, claudeAskRecord)
	writeDialogStatus(t, m, sess.ID, claudeAskStatus)
	key := messageSend(1)
	if !m.poller.reserveSend(key) {
		t.Fatal("no send slot")
	}
	settled := make(chan error, 1)
	m.poller.runSend(sess, key, "the child finished its turn", func(err error) error {
		settled <- err
		return nil
	})
	select {
	case err := <-settled:
		if !errors.Is(err, tmux.ErrSubmitHeld) {
			t.Fatalf("send = %v, want the Enter held", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the send never settled")
	}
}
