package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
)

func operatorInputs(r *recordingObserver) []extension.OperatorInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]extension.OperatorInput(nil), r.inputs...)
}

// A line sent from the prompt bar is reported once it has reached the pane,
// with the words the operator wrote.
func TestQuickPromptSendIsReportedAsTheOperatorsInput(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "answer-me", t.TempDir(), "")
	sess := m.sessionRows()[0]
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.selectSessionRow(t, "answer-me")

	m.openQuickMode()
	m.quick.input.SetValue("carry on with the plan")
	if _, _ = m.submitQuick(); m.errBar.text != "" {
		t.Fatalf("send: %q", m.errBar.text)
	}
	got := operatorInputs(observer)
	if len(got) != 1 {
		t.Fatalf("reported %+v, want one input", got)
	}
	if got[0].SessionID != sess.ID || got[0].Via != extension.OperatorPrompt ||
		got[0].Text != "carry on with the plan" || got[0].Dialog || got[0].At.IsZero() {
		t.Fatalf("reported %+v, want the prompt line to %s", got[0], sess.ID)
	}
}

// A send the board refuses reached nobody, so nobody is told a person
// answered.
func TestRefusedQuickPromptIsNotReported(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "gone", t.TempDir(), "")
	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.selectSessionRow(t, "gone")

	m.openQuickMode()
	m.quick.input.SetValue("hello?")
	m.submitQuick()
	if got := operatorInputs(observer); len(got) != 0 {
		t.Fatalf("a refused send was reported: %+v", got)
	}
}

func TestSnippetSendIsReportedAndARefusedOneIsNot(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "held": status.Waiting})
	m.rebuildRows()
	observer := &recordingObserver{}
	m.ObserveBoard(observer)

	held := sessionNamed(t, m, "held")
	paintDialog(t, m, held.ID)
	m.selectSessionRow(t, "held")
	sendPlusMinusToSelected(t, m)

	ask := sessionNamed(t, m, "ask")
	m.selectSessionRow(t, "ask")
	sendPlusMinusToSelected(t, m)

	got := operatorInputs(observer)
	if len(got) != 1 || got[0].SessionID != ask.ID || got[0].Via != extension.OperatorSnippet || got[0].Text != plusMinusText {
		t.Fatalf("reported %+v, want only the snippet that reached %s", got, ask.ID)
	}
}

// A key in a focused pane that chooses from its dialog is the operator
// answering it; the board passes keys on, so there is no line to report.
// A key on the way to an answer is not reported at all.
func TestFocusedDialogAnswerIsReportedAsADialogAnswer(t *testing.T) {
	m := buildModel(t)
	m.autoProceed = false
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.triage = true
	m.rebuildRows()
	observer := &recordingObserver{}
	m.ObserveBoard(observer)

	m.enterFocusOn(t, "ask")
	id := focusedID(t, m)
	stageDialog(m, id)
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(*Model)
	if got := operatorInputs(observer); len(got) != 0 {
		t.Fatalf("a key that answers nothing was reported: %+v", got)
	}
	stageDialog(m, id)
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := operatorInputs(observer)
	if len(got) != 1 || got[0].SessionID != id || got[0].Via != extension.OperatorPane || !got[0].Dialog || got[0].Text != "" {
		t.Fatalf("reported %+v, want one dialog answer in %s's pane", got, id)
	}
}
