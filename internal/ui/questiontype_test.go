package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/snippets"
	"github.com/usestring/gate-inbox/internal/status"
)

// paintQuestion puts an AskUserQuestion dialog with a free-text row on the
// session's real pane and switches the fleet to the tool whose rules read it.
func paintQuestion(t *testing.T, m *Model, sessID string) {
	t.Helper()
	for i := range m.sessions {
		m.sessions[i].Tool = "claude-hooked"
	}
	m.rebuildRows()
	rule := strings.Repeat("─", 60)
	question := strings.Join([]string{
		rule,
		" ☐ Next step",
		"",
		"What should I pick up?",
		"",
		"❯ 1. Run it locally",
		"  2. Set up the repo",
		"  3. Type something.",
		rule,
		"  4. Chat about this",
		"",
		"Enter to select · ↑/↓ to navigate · Esc to cancel",
	}, "\n") + "\n"
	// The pane is a shell, so the frame is printed from a file rather than
	// typed, where each line would run as a command.
	frame := filepath.Join(t.TempDir(), "question.txt")
	if err := os.WriteFile(frame, []byte(question), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.tmux.SendText(sessID, "clear; cat "+frame); err != nil {
		t.Fatalf("paint the question: %v", err)
	}
	waitForPaneText(t, m, sessID, "Esc to cancel")
}

// typerCalls records what a stub QuestionTyper was handed.
type typerCalls struct {
	ids, words []string
	// standing is the questions the dialog is left waiting on.
	standing int
	err      error
}

func (c *typerCalls) typer(id, words string) (sessioncmd.AnsweredQuestion, error) {
	c.ids, c.words = append(c.ids, id), append(c.words, words)
	return sessioncmd.AnsweredQuestion{Verified: c.err == nil, Standing: c.standing}, c.err
}

// A snippet sent to a session on a structured question goes into the
// question's free-text row instead of being held back.
func TestSnippetAnswersAQuestionThroughItsFreeTextRow(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	calls := &typerCalls{}
	m.UseQuestionTyper(calls.typer)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	if err := m.store.SetAcked(sess.ID, true); err != nil {
		t.Fatalf("set acked: %v", err)
	}
	paintQuestion(t, m, sess.ID)
	m.selectSessionRow(t, "ask")

	snip, _ := m.snippetFor(snippets.PlusMinusKey)
	_, cmd := m.sendSnippetToSelected(snip)
	if cmd == nil {
		t.Fatalf("no answer was started: %s", m.errBar.text)
	}
	m.applyCmd(t, cmd)
	if len(calls.ids) != 1 || calls.ids[0] != sess.ID || calls.words[0] != plusMinusText {
		t.Fatalf("typer got ids %v words %v", calls.ids, calls.words)
	}
	if !strings.Contains(m.errBar.text, "answered") {
		t.Fatalf("err = %q, want the answer reported", m.errBar.text)
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Acked {
		t.Fatal("answering the question left the acked flag set")
	}
}

// A failed answer says why, rather than reading as sent.
func TestSnippetSaysWhenAQuestionWouldNotTakeIt(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	calls := &typerCalls{err: errors.New("the dialog moved")}
	m.UseQuestionTyper(calls.typer)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	paintQuestion(t, m, sess.ID)
	m.selectSessionRow(t, "ask")

	snip, _ := m.snippetFor(snippets.PlusMinusKey)
	_, cmd := m.sendSnippetToSelected(snip)
	if cmd == nil {
		t.Fatalf("no answer was started: %s", m.errBar.text)
	}
	m.applyCmd(t, cmd)
	if !strings.Contains(m.errBar.text, "could not answer") || !strings.Contains(m.errBar.text, "the dialog moved") {
		t.Fatalf("err = %q, want the failure named", m.errBar.text)
	}
	if m.latestSubmission.sessionID != "" {
		t.Fatal("an answer that did not land was recorded as a submission")
	}
}

// A second answer to the same question, started before the first is read
// back, is refused rather than typed over it.
func TestSnippetAnswersAQuestionOneAtATime(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	calls := &typerCalls{}
	m.UseQuestionTyper(calls.typer)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	paintQuestion(t, m, sess.ID)
	m.selectSessionRow(t, "ask")

	snip, _ := m.snippetFor(snippets.PlusMinusKey)
	_, first := m.sendSnippetToSelected(snip)
	if first == nil {
		t.Fatalf("no answer was started: %s", m.errBar.text)
	}
	if _, second := m.sendSnippetToSelected(snip); second != nil {
		t.Fatal("a second answer was started while the first was being read back")
	}
	if !strings.Contains(m.errBar.text, "still answering") {
		t.Fatalf("err = %q, want the second answer refused", m.errBar.text)
	}
	m.applyCmd(t, first)
	if len(calls.ids) != 1 {
		t.Fatalf("typer called %d times, want once", len(calls.ids))
	}
	if _, again := m.sendSnippetToSelected(snip); again == nil {
		t.Fatalf("once the first answer was read back, another was refused: %s", m.errBar.text)
	}
}

// Offline, a question answer is refused: nothing can hold it for the network.
func TestSnippetRefusesToAnswerAQuestionOffline(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	calls := &typerCalls{}
	m.UseQuestionTyper(calls.typer)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	paintQuestion(t, m, sess.ID)
	m.selectSessionRow(t, "ask")
	goOffline(t, m)

	snip, _ := m.snippetFor(snippets.PlusMinusKey)
	if _, cmd := m.sendSnippetToSelected(snip); cmd != nil {
		t.Fatal("an answer was started offline")
	}
	if !strings.Contains(m.errBar.text, "offline") || len(calls.ids) != 0 {
		t.Fatalf("err = %q, typer calls %v; want the offline refusal and no typing", m.errBar.text, calls.ids)
	}
	if m.latestSubmission.sessionID != "" {
		t.Fatal("a refused answer was recorded as a submission")
	}
}

// A permission prompt has no free-text row: the snippet stays refused there,
// typer or not.
func TestSnippetStillRefusesAPermissionPromptWithATyper(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	calls := &typerCalls{}
	m.UseQuestionTyper(calls.typer)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	paintDialog(t, m, sess.ID)
	m.selectSessionRow(t, "ask")

	sendPlusMinusToSelected(t, m)
	if !strings.Contains(m.errBar.text, "dialog") || len(calls.ids) != 0 {
		t.Fatalf("err = %q, typer calls %v; want the dialog refusal and no typing", m.errBar.text, calls.ids)
	}
}

// A snippet answering the focused session's question is a whole answer like
// any other: auto-proceed hands the session over once it is read back, and
// the answer is the one undo takes back.
func TestSnippetAnsweringAQuestionHandsOverAndIsUndoable(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	calls := &typerCalls{}
	m.UseQuestionTyper(calls.typer)
	m.autoProceed = true
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.triage = true
	m.rebuildRows()
	askID := sessionNamed(t, m, "ask").ID
	paintQuestion(t, m, askID)
	m.enterFocusOn(t, "ask")

	snip, _ := m.snippetFor(snippets.PlusMinusKey)
	_, cmd := m.sendSnippetToFocused(snip)
	if cmd == nil {
		t.Fatalf("no answer was started: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("starting the answer handed the session over to %q", got)
	}
	m.applyCmd(t, cmd)
	if len(calls.ids) != 1 || calls.ids[0] != askID {
		t.Fatalf("typer got ids %v", calls.ids)
	}
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("after the answer was read back, focused %q want %q", got, "next")
	}
	if m.latestSubmission.sessionID != askID {
		t.Fatalf("undo targets %q, want the answered session %q", m.latestSubmission.sessionID, askID)
	}
}

// An answer that leaves the operator on the session is not handed over: they
// went on typing while it was read back, or the dialog moved to its next
// question.
func TestSnippetAnsweringAQuestionStaysWhenTheSessionIsNotDone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		standing int
		typing   bool
	}{
		{name: "operator kept typing", typing: true},
		{name: "questions still standing", standing: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := buildModel(t)
			bindPlusMinus(t, m)
			calls := &typerCalls{standing: tc.standing}
			m.UseQuestionTyper(calls.typer)
			m.autoProceed = true
			liveTriageFleet(t, m, map[string]string{
				"ask":  status.Waiting,
				"next": status.Waiting,
			})
			m.triage = true
			m.rebuildRows()
			paintQuestion(t, m, sessionNamed(t, m, "ask").ID)
			m.enterFocusOn(t, "ask")

			snip, _ := m.snippetFor(snippets.PlusMinusKey)
			_, cmd := m.sendSnippetToFocused(snip)
			if cmd == nil {
				t.Fatalf("no answer was started: %s", m.errBar.text)
			}
			if tc.typing {
				m.keepLandingHere()
			}
			m.applyCmd(t, cmd)
			if got := focusedName(t, m); got != "ask" {
				t.Fatalf("focused %q, want the answer to leave the operator on %q (%s)", got, "ask", m.errBar.text)
			}
			if tc.standing > 0 && !strings.Contains(m.errBar.text, "1 more to answer") {
				t.Fatalf("err = %q, want the standing question named", m.errBar.text)
			}
		})
	}
}
