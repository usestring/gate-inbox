package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
)

func operatorInputs(r *recordingObserver) []extension.OperatorInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]extension.OperatorInput(nil), r.inputs...)
}

// A snippet sent from the hotkey menu is reported once it has reached the
// pane, with the words the snippet sends.
func TestHotkeyMenuSendIsReportedAsTheOperatorsInput(t *testing.T) {
	m := buildModel(t)
	bindMenuSnippet(t, m, "carry on with the plan")
	createSession(t, m, "answer-me", t.TempDir(), "")
	sess := m.sessionRows()[0]
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.selectSessionRow(t, "answer-me")

	m.openQuickMode()
	if _, _ = m.handleQuickKey(letter('c')); !strings.HasPrefix(m.errBar.text, "sent ") {
		t.Fatalf("send: %q", m.errBar.text)
	}
	if got := operatorInputs(observer); len(got) != 0 {
		t.Fatalf("the snippet was reported before it landed: %+v", got)
	}
	landAnswer(t, m, sess.ID)
	got := operatorInputs(observer)
	if len(got) != 1 {
		t.Fatalf("reported %+v, want one input", got)
	}
	if got[0].SessionID != sess.ID || got[0].Via != extension.OperatorSnippet ||
		got[0].Text != "carry on with the plan" || got[0].Dialog || got[0].At.IsZero() {
		t.Fatalf("reported %+v, want the snippet to %s", got[0], sess.ID)
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

	if _, armed := m.landings[held.ID]; armed {
		t.Fatal("a refused send was left waiting to land")
	}
	landAnswer(t, m, ask.ID)
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
	if got := operatorInputs(observer); len(got) != 0 {
		t.Fatalf("the key was reported before the answer landed: %+v", got)
	}
	recordDialogResult(t, m, id, "answered")
	lookForLanding(t, m)
	got := operatorInputs(observer)
	if len(got) != 1 || got[0].SessionID != id || got[0].Via != extension.OperatorPane || !got[0].Dialog || got[0].Text != "" {
		t.Fatalf("reported %+v, want one dialog answer in %s's pane", got, id)
	}
}

// A line typed and pasted into a focused pane is reported with how it was
// written: the board never holds the line, so these counts are all an
// extension has to measure the operator's writing by.
func TestFocusedLineIsReportedWithHowItWasWritten(t *testing.T) {
	m := buildModel(t)
	m.autoProceed = false
	liveTriageFleet(t, m, map[string]string{"ask": status.Idle})
	m.rebuildRows()
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.enterFocusOn(t, "ask")
	id := focusedID(t, m)

	for _, r := range "fix thsi" {
		m.handleFocusKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	for range 4 {
		m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	m.handleFocusPaste(tea.PasteMsg{Content: " the failing test"})
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	landAnswer(t, m, id)

	got := operatorInputs(observer)
	if len(got) != 1 || got[0].Via != extension.OperatorPane || got[0].Text != "" {
		t.Fatalf("reported %+v, want one pane line with no text", got)
	}
	w := got[0].Writing
	if w.KeyChars != 8 || w.PasteChars != 17 || w.Pastes != 1 || w.Words != 4 {
		t.Fatalf("writing %+v, want 8 typed and 17 pasted characters making 4 words", w)
	}
	if w.Compose < w.Active {
		t.Fatalf("active %v is longer than the compose time %v", w.Active, w.Compose)
	}

	// The next line starts from nothing.
	m.handleFocusKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	landAnswer(t, m, id)
	if got := operatorInputs(observer); len(got) != 2 || got[1].Writing.KeyChars != 1 || got[1].Writing.Words != 1 {
		t.Fatalf("second line reported %+v, want one typed character", got[len(got)-1])
	}
}

// Active caps each pause, and a run of keys faster than anyone types is a
// burst. Measured against a fixed clock, since the board stamps each key as
// it arrives.
func TestLineWritingCapsPausesAndFindsBursts(t *testing.T) {
	start := time.Now()
	at := func(d time.Duration) time.Time { return start.Add(d) }
	w := &lineWriting{}
	w.key(at(0), at(0), "o", "")
	w.key(at(200*time.Millisecond), at(200*time.Millisecond), "k", "")
	w.key(at(400*time.Millisecond), at(400*time.Millisecond), " ", "")
	// A long think, then text typed out as keys in one read. The first of
	// them is slow to forward, and the rest queue behind it.
	burst := at(time.Minute)
	for i, r := range "and more" {
		arrived, done := burst, burst.Add(30*time.Millisecond)
		if i > 0 {
			arrived = done.Add(time.Duration(i) * time.Millisecond)
			done = arrived
		}
		w.key(arrived, done, string(r), "")
	}
	w.key(burst.Add(40*time.Millisecond), burst.Add(40*time.Millisecond), "", "C-w")
	got := w.finish(burst.Add(time.Second))

	if got.KeyChars != 11 || got.BurstChars != 8 || got.Words != 2 {
		t.Fatalf("writing %+v, want 11 keys, 8 of them a burst, 2 words left", got)
	}
	wantActive := 400*time.Millisecond + extension.OperatorPauseCap + 40*time.Millisecond + 960*time.Millisecond
	if got.Active != wantActive || got.Compose != time.Minute+time.Second {
		t.Fatalf("active %v compose %v, want %v and the whole span", got.Active, got.Compose, wantActive)
	}
}
