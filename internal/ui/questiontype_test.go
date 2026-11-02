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
	err        error
}

func (c *typerCalls) typer(id, words string) (sessioncmd.AnsweredQuestion, error) {
	c.ids, c.words = append(c.ids, id), append(c.words, words)
	return sessioncmd.AnsweredQuestion{Verified: c.err == nil}, c.err
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
