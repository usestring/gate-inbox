package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/snippets"
	"github.com/usestring/gate-inbox/internal/status"
)

// These pin the guarded send every snippet goes through, driven from the bare
// ± binding: the one snippet key with no modifier, and so the one that has to
// get past handlers that read unmodified keys as their own.

const plusMinusText = "merge it and ship it"

func plusMinusMsg() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(snippets.PlusMinusKey)[0], Text: snippets.PlusMinusKey}
}

// bindPlusMinus writes a snippet file whose only entry is on ±.
func bindPlusMinus(t *testing.T, m *Model) {
	t.Helper()
	writeSnippets(t, m, []snippets.Snippet{{Key: snippets.PlusMinusKey, Text: plusMinusText}})
}

// sendPlusMinusToSelected is ± pressed on the list, without the list handler
// around it.
func sendPlusMinusToSelected(t *testing.T, m *Model) {
	t.Helper()
	snip, ok := m.snippetFor(snippets.PlusMinusKey)
	if !ok {
		t.Fatal("± did not resolve to its snippet")
	}
	m.sendSnippetToSelected(snip)
}

// The sentence arrives in the pane, submitted, from one bare key inside the
// session.
//
// With the handover turned off, so the subject is the send alone. ± is an
// answer and hands the session over on the default setting, which is what
// TestPlusMinusSnippetHandsOverWhenAutoProceedIsOn covers.
func TestPlusMinusSnippetKeySendsTheLineIntoTheFocusedPane(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	m.autoProceed = false
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, _ := m.handleFocusKey(plusMinusMsg())
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("± left the session, mode %v: %s", m.mode, m.errBar.text)
	}
	waitForPaneText(t, m, sess.ID, plusMinusText)
}

// ± is a whole answer, so on the default setting it spends it the way an
// answered dialog does: the session is handed over and the next one that
// needs a person opens. This is the path a drain actually takes now, and
// nothing covered it while the setting shipped off.
func TestPlusMinusSnippetHandsOverWhenAutoProceedIsOn(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	if !m.autoProceed {
		t.Fatal("a fresh model came up with the handover off")
	}
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, _ := m.handleFocusKey(plusMinusMsg())
	m = updated.(*Model)

	waitForPaneText(t, m, sess.ID, plusMinusText)
	if m.mode != modeFocus {
		t.Fatalf("± dropped out of the queue, mode %v: %s", m.mode, m.errBar.text)
	}
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("after ±, focused %q want %q", got, "next")
	}
}

// Focused, a ± snippet is claimed whether or not triage is on: it answers the session
// in front of the operator rather than walking a queue.
func TestPlusMinusSnippetKeySendsOutsideTriage(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, _ := m.handleFocusKey(plusMinusMsg())
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("± outside triage left the session, mode %v", m.mode)
	}
	waitForPaneText(t, m, sess.ID, plusMinusText)
}

// The same key from the list, so a queue can be answered without entering
// each session first.
func TestPlusMinusSnippetKeyFromTheListSendsToTheCursorRow(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "other": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	sess := sessionNamed(t, m, "ask")

	updated, cmd := m.handleKey(plusMinusMsg())
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeList {
		t.Fatalf("± on the list changed mode to %v", m.mode)
	}
	waitForPaneText(t, m, sess.ID, plusMinusText)

	// It answers the row under the cursor and nothing else: a key that
	// sprayed the fleet would be unusable.
	other := sessionNamed(t, m, "other")
	pane, err := m.tmux.CapturePane(other.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if strings.Contains(squashSpace(ansi.Strip(pane)), squashSpace(plusMinusText)) {
		t.Fatalf("± also answered %q:\n%s", other.Name, pane)
	}
}

// An answered session is one the operator is waiting on again, so the alert
// it raises when it finishes must not be suppressed by an old ack.
func TestPlusMinusSnippetClearsTheAckedFlag(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	if err := m.store.SetAcked(sess.ID, true); err != nil {
		t.Fatalf("set acked: %v", err)
	}
	m.selectSessionRow(t, "ask")

	sendPlusMinusToSelected(t, m)
	if m.errBar.text == "" {
		t.Fatal("a send says so on the error bar")
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Acked {
		t.Fatal("± left the acked flag set")
	}
}

// SendText pastes and presses Enter, so on a shell the sentence would run as
// a command. Same guard the quick prompt has, and for the same reason.
func TestPlusMinusSnippetRefusesAShell(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	m.applyCmd(t, m.refreshCmd())
	sess := spawnTerminal(t, m)
	m.selectSessionRow(t, sess.Name)

	sendPlusMinusToSelected(t, m)
	if m.errBar.text != shellPromptHint(sess.Name) {
		t.Fatalf("err = %q, want the shell refusal", m.errBar.text)
	}
}

// paintDialog puts a permission dialog on the session's real pane -- the test
// tools are cat, so what is sent comes back -- and switches the fleet to the
// tool whose rules can read it. dialogHold reads the pane rather than the
// board's cached preview, so a staged preview would not exercise it.
func paintDialog(t *testing.T, m *Model, sessID string) {
	t.Helper()
	for i := range m.sessions {
		m.sessions[i].Tool = "claude-hooked"
	}
	m.rebuildRows()
	dialog := strings.Join([]string{
		"  Do you want to proceed?",
		"❯ 1. Yes",
		"  2. No",
		"  Enter to confirm · Esc to cancel",
	}, "\n")
	if err := m.tmux.SendText(sessID, dialog); err != nil {
		t.Fatalf("paint the dialog: %v", err)
	}
	waitForPaneText(t, m, sessID, "Enter to confirm")
}

// The defect this guard exists for: SendText pastes and presses Enter, so ±
// on a session showing a dialog answered the dialog rather than being read.
// The refusal has to say so, since a silent no-op reads as a broken key.
func TestPlusMinusSnippetRefusesAPaneShowingADialog(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	paintDialog(t, m, sess.ID)
	m.selectSessionRow(t, "ask")

	sendPlusMinusToSelected(t, m)
	if !strings.Contains(m.errBar.text, "dialog") {
		t.Fatalf("err = %q, want the dialog refusal", m.errBar.text)
	}
	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if strings.Contains(squashSpace(ansi.Strip(pane)), squashSpace(plusMinusText)) {
		t.Fatalf("± reached the dialog anyway:\n%s", pane)
	}
}

// A question dialog replaces the tool's input box, so the pane has no prompt
// line at all. That is the shape the live board wedged on, and the read has to
// be the rules rather than the poller's TypingHold, which calls a pane with no
// input line "working" before it consults a rule and would send.
func TestPlusMinusSnippetRefusesADialogThatReplacedTheInputLine(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	for i := range m.sessions {
		m.sessions[i].Tool = "claude-hooked"
	}
	m.rebuildRows()
	full := strings.Join([]string{
		"  How should a parked session present on the board?",
		"  1. New status: delegating",
		"  2. Badge on the existing row",
		"  Enter to confirm · Esc to cancel",
	}, "\n")
	if err := m.tmux.SendText(sess.ID, full); err != nil {
		t.Fatalf("paint the dialog: %v", err)
	}
	waitForPaneText(t, m, sess.ID, "Enter to confirm")
	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, ready := m.engine.ActivityRegion("claude-hooked", ansi.Strip(pane)); ready {
		t.Fatal("the frame still has an input line, so this is not testing the no-prompt shape")
	}
	m.selectSessionRow(t, "ask")

	sendPlusMinusToSelected(t, m)
	if !strings.Contains(m.errBar.text, "dialog") {
		t.Fatalf("err = %q, want the dialog refusal", m.errBar.text)
	}
}

// The same refusal from inside the session, which is where a drain presses it.
func TestPlusMinusSnippetRefusesADialogFromFocus(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "next": status.Waiting})
	m.triage = true
	m.autoProceed = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")
	paintDialog(t, m, sess.ID)

	updated, _ := m.handleFocusKey(plusMinusMsg())
	m = updated.(*Model)
	if !strings.Contains(m.errBar.text, "dialog") {
		t.Fatalf("err = %q, want the dialog refusal", m.errBar.text)
	}
	// A refused send has left the session unanswered, so auto-proceed must
	// not have carried the operator on to the next one -- the refusal would
	// go off screen with it.
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("a refused ± handed the session over to %q", got)
	}
}

// A group is not a pane. The key looks broken if it answers with silence.
func TestPlusMinusSnippetOnAGroupSaysWhatToSelect(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	groupAt(t, m, "work", t.TempDir())

	sendPlusMinusToSelected(t, m)
	if m.errBar.text == "" {
		t.Fatal("± on a group said nothing")
	}
}

// The gate's menu reads unmodified keys as its own letters and swallows the
// rest, so a bare ± has to be let through by name or it never reaches the
// snippet.
func TestPlusMinusSnippetReachesThePaneThroughTheGateMenu(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	bindPlusMinus(t, m)
	m.gate.menu = true
	sess, ok := m.selected()
	if !ok {
		t.Fatal("the gate opened on nothing")
	}

	m = pressFocused(t, m, plusMinusMsg())
	waitForPaneText(t, m, sess.ID, plusMinusText)
}

// In the quick prompt a bare ± is a character the operator is typing, so it
// lands in the input rather than firing the snippet on it.
func TestPlusMinusTypesIntoTheQuickPrompt(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	m.openQuickMode()
	m.quick.input.SetValue("5 ")
	updated, cmd := m.handleKey(plusMinusMsg())
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := m.quick.input.Value(); got != "5 ±" {
		t.Fatalf("the quick prompt reads %q, want the ± typed", got)
	}
	if strings.Contains(m.errBar.text, plusMinusText) {
		t.Fatalf("± in the quick prompt fired the snippet: %q", m.errBar.text)
	}
}
