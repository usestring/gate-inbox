package ui

import (
	"fmt"
	"strings"
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

func paneInputs(r *recordingObserver) []extension.PaneInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]extension.PaneInput(nil), r.paneInputs...)
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
	if inputs := paneInputs(observer); len(inputs) != 1 || inputs[0].SessionID != ask.ID {
		t.Fatalf("pane input %+v, want only the snippet that reached %s", inputs, ask.ID)
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

// Keys and pastes in a focused pane are reported as they reach it, each key
// with when the board was free for it. The key that sends the line says so,
// and the line is reported under that key's At once it lands.
func TestFocusedKeysAndPastesAreReportedAsPaneInput(t *testing.T) {
	m := buildModel(t)
	m.autoProceed = false
	liveTriageFleet(t, m, map[string]string{"ask": status.Idle})
	m.rebuildRows()
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.enterFocusOn(t, "ask")
	id := focusedID(t, m)

	m.handleFocusKey(tea.KeyPressMsg{Code: 'o', Text: "o"})
	m.View()
	m.handleFocusKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m.handleFocusKey(tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt})
	m.handleFocusPaste(tea.PasteMsg{Content: " the failing test"})
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	got := paneInputs(observer)
	if len(got) != 6 {
		t.Fatalf("reported %+v, want six inputs", got)
	}
	for _, in := range got {
		if in.SessionID != id || in.Dialog || in.At.IsZero() {
			t.Fatalf("reported %+v, want input to %s's prompt", in, id)
		}
	}
	o, k, bs, altB, paste, enter := got[0], got[1], got[2], got[3], got[4], got[5]
	if o.Text != "x" || o.Key != "" || o.Paste || o.Sends || !o.Ready.IsZero() {
		t.Fatalf("first key %+v, want one character with no key before it", o)
	}
	// The screen drawn after the first key moved the board's ready on past it.
	if k.Text != "x" || !k.Ready.After(o.At) || k.Ready.After(k.At) {
		t.Fatalf("second key %+v, want a character ready after the first at %v", k, o.At)
	}
	// Nothing was drawn after the second, so it was done with it as it came.
	if bs.Text != "" || bs.Key != "BSpace" || !bs.Ready.Equal(k.At) {
		t.Fatalf("backspace %+v, want it ready at the second key's %v", bs, k.At)
	}
	if altB.Text != "" || altB.Key != "M-x" {
		t.Fatalf("alt+b %+v, want it named M-x, its character masked", altB)
	}
	if paste.Text != " xxx xxxxxxx xxxx" || !paste.Paste || paste.Key != "" || !paste.Ready.IsZero() {
		t.Fatalf("paste %+v", paste)
	}
	if enter.Key != "Enter" || !enter.Sends || !enter.Ready.IsZero() {
		t.Fatalf("enter %+v, want the key that sends, with no key since the paste", enter)
	}

	landAnswer(t, m, id)
	inputs := operatorInputs(observer)
	if len(inputs) != 1 || inputs[0].Via != extension.OperatorPane || !inputs[0].At.Equal(enter.At) {
		t.Fatalf("reported %+v, want the line under the Enter's %v", inputs, enter.At)
	}
}

// A key on a dialog is reported with it, and only the one that answers it
// sends; the line typed before the dialog came up was typed at the prompt.
func TestKeysOnADialogAreReportedWithIt(t *testing.T) {
	m := buildModel(t)
	m.autoProceed = false
	liveTriageFleet(t, m, map[string]string{"ask": status.Idle})
	m.rebuildRows()
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.enterFocusOn(t, "ask")
	id := focusedID(t, m)

	m.handleFocusKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	stageDialog(m, id)
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	got := paneInputs(observer)
	if len(got) != 3 {
		t.Fatalf("reported %+v, want three keys", got)
	}
	if got[0].Text != "x" || got[0].Dialog || got[0].Sends {
		t.Fatalf("first key %+v, want a character typed at the prompt", got[0])
	}
	if got[1].Key != "Down" || !got[1].Dialog || got[1].Sends {
		t.Fatalf("second key %+v, want an arrow on the dialog", got[1])
	}
	if got[2].Key != "Enter" || !got[2].Dialog || !got[2].Sends {
		t.Fatalf("third key %+v, want the Enter that answers the dialog", got[2])
	}
}

// A snippet the board types into a pane is reported as a paste: left in the
// line for the operator's Enter, or sent whole, which sends the line and is
// reported under the snippet's At once it lands.
func TestSnippetTypedIntoAPaneIsReportedAsAPaste(t *testing.T) {
	m := buildModel(t)
	bindMenuSnippet(t, m, "carry on with the plan")
	createSession(t, m, "answer-me", t.TempDir(), "")
	sess := m.sessionRows()[0]
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.selectSessionRow(t, "answer-me")

	m.sendSentence(sess, "and then", `"and then"`, false, false)
	m.openQuickMode()
	if _, _ = m.handleQuickKey(letter('c')); !strings.HasPrefix(m.errBar.text, "sent ") {
		t.Fatalf("send: %q", m.errBar.text)
	}

	got := paneInputs(observer)
	if len(got) != 2 {
		t.Fatalf("reported %+v, want the typed sentence and the sent snippet", got)
	}
	if got[0].SessionID != sess.ID || got[0].Text != "xxx xxxx" || !got[0].Paste || got[0].Sends {
		t.Fatalf("typed sentence %+v, want a paste left in the line", got[0])
	}
	if got[1].SessionID != sess.ID || got[1].Text != "xxxxx xx xxxx xxx xxxx" || !got[1].Paste || !got[1].Sends {
		t.Fatalf("sent snippet %+v, want a paste that sends", got[1])
	}
	landAnswer(t, m, sess.ID)
	inputs := operatorInputs(observer)
	if len(inputs) != 1 || !inputs[0].At.Equal(got[1].At) {
		t.Fatalf("reported %+v, want the snippet under its pane input's %v", inputs, got[1].At)
	}
}

// What the operator types or pastes reaches extensions as its shape only: a
// password typed at a prompt that doesn't echo, or a pasted key, keeps its
// length and word breaks and loses its characters. A character typed with
// alt, as one typed just after Escape arrives, loses its character from
// the key's name.
func TestASecretTypedOrPastedNeverReachesAnExtension(t *testing.T) {
	m := buildModel(t)
	m.autoProceed = false
	liveTriageFleet(t, m, map[string]string{"ask": status.Idle})
	m.rebuildRows()
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.enterFocusOn(t, "ask")

	for _, r := range "hunter2 é!" {
		m.handleFocusKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.handleFocusKey(tea.KeyPressMsg{Code: 'é', Mod: tea.ModAlt})
	m.handleFocusPaste(tea.PasteMsg{Content: "sk-live-AbC123\tok\n"})
	m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	got := paneInputs(observer)
	var typed, pasted, keys strings.Builder
	for _, in := range got {
		switch {
		case in.Paste:
			pasted.WriteString(in.Text)
		case in.Key != "":
			keys.WriteString(in.Key + " ")
		default:
			typed.WriteString(in.Text)
		}
	}
	if typed.String() != "xxxxxxx xx" || pasted.String() != "xxxxxxxxxxxxxx\txx\n" || keys.String() != "M-x Enter " {
		t.Fatalf("typed %q, pasted %q and keys %q, want only their shapes", typed.String(), pasted.String(), keys.String())
	}
	if seen := fmt.Sprintf("%+v", got); strings.Contains(seen, "hunter") || strings.Contains(seen, "sk-live") {
		t.Fatalf("an extension received the secret: %s", seen)
	}
}
