package ui

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
)

// The legend the pi extension's AskUserQuestion draws, which no pane rule
// knows: to the rules the pane is a resting prompt.
const piQuestionPane = "Approval\nRun the migration?\n→ Yes\n  No\nEnter to select · ↑/↓ to navigate · Esc to cancel\n❯ "

// A pi session whose extension has said it drew a dialog takes no message,
// whatever its pane shows: the paste's Enter would pick the focused option,
// which pi records as the user's answer. Once the extension says the dialog
// closed, the same message goes in.
func TestInboxHoldsAMessageWhileAPiExtensionDialogIsOpen(t *testing.T) {
	m := buildModel(t)
	m.poller.statusSources["ready-tool"] = hooks.StatusSourcePi
	sess := spawnedSession(t, m, "ready-tool")
	settledPane(t, m, sess.ID, "❯")
	queueMessage(t, m, sess.ID, "the child stopped on a permission prompt")

	if state, matched := m.poller.engine.RuleMatch(sess.Tool, piQuestionPane); matched && state == status.Waiting {
		t.Fatal("fixture no longer reproduces the hazard: no rule may see the dialog")
	}
	writeHookStatus(t, m, sess.ID, "working agent_start\nwaiting "+hooks.PiPromptStart+"\n")
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), piQuestionPane, status.Idle, true); err != nil {
		t.Fatalf("maybeDeliverInbox: %v", err)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 1 {
		t.Fatal("a message was typed into a pi dialog its extension reported open")
	}
	if pane, _ := m.tmux.CapturePane(sess.ID); strings.Contains(pane, "permission prompt") {
		t.Fatalf("message reached the pane:\n%s", pane)
	}

	writeHookStatus(t, m, sess.ID, "working agent_start\nwaiting "+hooks.PiPromptStart+"\nidle ui_prompt_end\n")
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), "❯ ", status.Idle, true); err != nil {
		t.Fatalf("maybeDeliverInbox after the dialog: %v", err)
	}
	if queued, _ := m.store.QueuedCount(sess.ID); queued != 0 {
		t.Fatal("the message stayed held after the extension reported the dialog closed")
	}
}

// The hold is read only from a pi session's own hooks: another tool's
// status file naming the same event holds nothing.
func TestOnlyAPiSessionsHooksHoldOnAPromptStart(t *testing.T) {
	m := buildModel(t)
	sess := spawnedSession(t, m, "ready-tool")
	writeHookStatus(t, m, sess.ID, "waiting "+hooks.PiPromptStart+"\n")
	if m.poller.hookDialog(sess) {
		t.Fatal("a session that is not pi was held by a pi event")
	}
	m.poller.statusSources["ready-tool"] = hooks.StatusSourcePi
	if !m.poller.hookDialog(sess) {
		t.Fatal("a pi session's open dialog did not hold it")
	}
}

// An interrupt's Escape would cancel the dialog, so it waits for the dialog
// to close like the message does.
func TestInboxInterruptHoldsWhileAPiExtensionDialogIsOpen(t *testing.T) {
	m := buildModel(t)
	m.poller.statusSources["claude-typeahead"] = hooks.StatusSourcePi
	sess := spawnedSession(t, m, "claude-typeahead")
	id := queueInterrupt(t, m, sess.ID, "stop querying GCP, use Axiom")
	if err := m.tmux.SendKeys(sess.ID, "❯ "); err != nil {
		t.Fatal(err)
	}
	settledPane(t, m, sess.ID, "❯")

	writeHookStatus(t, m, sess.ID, "waiting "+hooks.PiPromptStart+"\n")
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), workingPane, status.Working, true); err != nil {
		t.Fatalf("maybeDeliverInbox: %v", err)
	}
	if interrupted(m, id) {
		t.Fatal("interrupt sent keys while a pi dialog was open")
	}

	writeHookStatus(t, m, sess.ID, "waiting "+hooks.PiPromptStart+"\nworking ui_prompt_end\n")
	if err := deliverInbox(t, m, sess, queuedHeads(t, m), workingPane, status.Working, true); err != nil {
		t.Fatalf("maybeDeliverInbox after the dialog: %v", err)
	}
	if !interrupted(m, id) {
		t.Fatal("interrupt stayed held after the dialog closed")
	}
}

// The operator's own send and menu letters read the same hold, so a line
// sent from the board is refused with the reason instead of answering the
// dialog.
func TestAnOperatorSendHoldsOnAPiExtensionDialog(t *testing.T) {
	m := buildModel(t)
	m.poller.statusSources["ready-tool"] = hooks.StatusSourcePi
	sess := spawnedSession(t, m, "ready-tool")
	settledPane(t, m, sess.ID, "❯")
	if hold, _ := m.dialogHold(sess); hold != "" {
		t.Fatalf("a resting pi session was held: %q", hold)
	}
	writeHookStatus(t, m, sess.ID, "waiting "+hooks.PiPromptStart+"\n")
	if hold, _ := m.dialogHold(sess); !strings.Contains(hold, "dialog open") {
		t.Fatalf("dialogHold = %q, want the open-dialog hold", hold)
	}
}
