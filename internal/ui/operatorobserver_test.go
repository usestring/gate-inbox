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
	if got[0].Writing != (extension.OperatorWriting{Words: 5, Chars: 22}) {
		t.Fatalf("writing %+v, want the sentence's 5 words and 22 characters and nothing typed", got[0].Writing)
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
	if w.KeyChars != 8 || w.PasteChars != 17 || w.Pastes != 1 || w.Words != 4 || w.Chars != 21 {
		t.Fatalf("writing %+v, want 8 typed and 17 pasted characters leaving 4 words in 21", w)
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

// A dialog that comes up over a half-written line is answered without taking
// the line with it, even when the operator arrows to the row before Enter:
// the line is still in the composer, and goes with the composer's own Enter
// once the dialog is gone.
func TestDialogAnswerLeavesTheComposersLine(t *testing.T) {
	m := buildModel(t)
	m.autoProceed = false
	liveTriageFleet(t, m, map[string]string{"ask": status.Idle})
	m.rebuildRows()
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.enterFocusOn(t, "ask")
	id := focusedID(t, m)

	for _, r := range "draft" {
		m.handleFocusKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	stageDialog(m, id)
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	landAnswer(t, m, id)
	m.preview, m.pane.cursor = "", paneCursor{}
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	landAnswer(t, m, id)

	got := operatorInputs(observer)
	if len(got) != 2 || !got[0].Dialog || got[0].Writing != (extension.OperatorWriting{}) {
		t.Fatalf("reported %+v, want the dialog's answer first, with nothing written", got)
	}
	if got[1].Dialog || got[1].Writing.KeyChars != 5 || got[1].Writing.Words != 1 {
		t.Fatalf("reported %+v, want the composer's line second, with its 5 typed characters", got[1])
	}
}

// Active caps each pause, a run of keys faster than anyone types is a burst,
// and ctrl+c abandons a line. Measured against a fixed clock, since the board
// stamps each key as it arrives.
func TestLineWritingCapsPausesAndFindsBursts(t *testing.T) {
	start := time.Now()
	at := func(d time.Duration) time.Time { return start.Add(d) }
	w := &lineWriting{}
	w.key(at(-10*time.Second), "gone", "", false)
	w.key(at(-9*time.Second), "", "C-c", false)
	w.key(at(0), "o", "", false)
	w.key(at(200*time.Millisecond), "k", "", false)
	w.key(at(400*time.Millisecond), " ", "", false)
	// A long think, then text typed out as keys in one go. The frame after
	// the first of them is slow to paint, and the rest wait behind it.
	burst := at(time.Minute)
	w.key(burst, "a", "", false)
	w.painted(burst.Add(30 * time.Millisecond))
	for i, r := range "nd more" {
		arrived := burst.Add(31*time.Millisecond + time.Duration(i)*time.Millisecond)
		w.key(arrived, string(r), "", false)
		w.painted(arrived)
	}
	w.key(burst.Add(500*time.Millisecond), "", "C-w", false)
	got := w.finish(burst.Add(time.Second))

	if got.KeyChars != 11 || got.BurstChars != 8 || got.Words != 2 || got.Chars != 7 {
		t.Fatalf("writing %+v, want 11 keys, 8 of them a burst, leaving 2 words in 7 characters", got)
	}
	wantActive := 400*time.Millisecond + extension.OperatorPauseCap + 37*time.Millisecond + 963*time.Millisecond
	if got.Active != wantActive || got.Compose != time.Minute+time.Second {
		t.Fatalf("active %v compose %v, want %v and the whole span", got.Active, got.Compose, wantActive)
	}
}
