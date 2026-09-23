package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The whole of auto-proceed rests on telling the key that answers a session
// from the keys on the way to it, so that is what this pins: the two gestures
// that hand a session over, and the near misses that must not.
func TestAnswersFocusedIsOnlyTheKeyThatAnswers(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	// Both dialogs are the shape the live board shows, caret on the marker.
	permission := []string{
		"  Do you want to proceed?",
		"❯ 1. Yes",
		"  2. No",
		"",
		"  Enter to confirm · Esc to cancel",
	}
	// A real multi-select: it steps between questions with the horizontal
	// arrows, so Left stays the dialog's -- but Enter still answers it, which
	// is why this reads off the dialog being up rather than off Left being
	// free. See leftLeavesFocus in focuskeys.go.
	multiSelect := []string{
		"  Which approach?",
		"❯ 1. [ ] Resume the three fix rounds (Recommended)",
		"  2. [ ] Merge both in order",
		"",
		"  Enter to select · Tab/Arrow keys to navigate · Esc to cancel",
	}
	written := []string{"", "❯ ship it"}
	empty := []string{"", "❯"}

	cases := []struct {
		name   string
		rows   []string
		cursor paneCursor
		key    tea.KeyPressMsg
		want   bool
	}{
		{"enter on a permission dialog", permission,
			paneCursor{x: 0, y: 1, ok: true}, tea.KeyPressMsg{Code: tea.KeyEnter}, true},
		{"a row's number on a permission dialog", permission,
			paneCursor{x: 0, y: 1, ok: true}, tea.KeyPressMsg{Code: '2', Text: "2"}, true},
		{"enter on a multi-select", multiSelect,
			paneCursor{x: 0, y: 1, ok: true}, tea.KeyPressMsg{Code: tea.KeyEnter}, true},
		// Space ticks a box in a multi-select. The question is still open.
		{"space on a multi-select", multiSelect,
			paneCursor{x: 0, y: 1, ok: true}, tea.KeyPressMsg{Code: ' ', Text: " "}, false},
		// A letter on a dialog is a keystroke the dialog may or may not want;
		// either way it is not the operator's last word on the session.
		{"a letter on a dialog", permission,
			paneCursor{x: 0, y: 1, ok: true}, tea.KeyPressMsg{Code: 'y', Text: "y"}, false},
		{"enter on a written message", written,
			paneCursor{x: 9, y: 1, ok: true}, tea.KeyPressMsg{Code: tea.KeyEnter}, true},
		// Shift+enter is how a message takes a newline: honouring it would
		// hand the session over mid-sentence.
		{"shift+enter on a written message", written,
			paneCursor{x: 9, y: 1, ok: true}, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, false},
		// Enter on an empty prompt sends nothing, so there is nothing to
		// proceed from.
		{"enter on an empty prompt", empty,
			paneCursor{x: 2, y: 1, ok: true}, tea.KeyPressMsg{Code: tea.KeyEnter}, false},
		{"a character being typed", written,
			paneCursor{x: 9, y: 1, ok: true}, tea.KeyPressMsg{Code: 'x', Text: "x"}, false},
	}
	sess := store.Session{ID: "s1", Tool: "claude"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &Model{engine: engine, mode: modeFocus}
			m.preview = strings.Join(c.rows, "\n") + "\n"
			m.pane.forID = "s1"
			m.pane.box.height, m.pane.box.width = len(c.rows), 80
			m.pane.cursor = c.cursor
			if got := m.answersFocused(sess, c.key); got != c.want {
				t.Fatalf("answersFocused = %v, want %v", got, c.want)
			}
		})
	}
}

// A pane the operator has scrolled back through is showing history, so the
// row under the caret says nothing about where the next key lands.
func TestAnswersFocusedIgnoresAScrolledBackPane(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	m := &Model{engine: engine, mode: modeFocus}
	m.preview = "\n❯ ship it\n"
	m.pane.forID = "s1"
	m.pane.box.height, m.pane.box.width = 2, 80
	m.pane.cursor = paneCursor{x: 9, y: 1, ok: true}
	sess := store.Session{ID: "s1", Tool: "claude"}
	if !m.answersFocused(sess, tea.KeyPressMsg{Code: tea.KeyEnter}) {
		t.Fatal("enter on a written message did not read as an answer")
	}
	m.focusScroll = 3
	if m.answersFocused(sess, tea.KeyPressMsg{Code: tea.KeyEnter}) {
		t.Fatal("a scrolled-back pane still read as an answer")
	}
}

// Off unless the operator asked for it, and only inside a drain: outside
// triage there is no queue to proceed along.
func TestAutoProceedIsOffAndScopedToADrain(t *testing.T) {
	m := &Model{}
	for _, c := range []struct {
		name         string
		auto, triage bool
		mode         mode
		want         bool
	}{
		{"default", false, true, modeFocus, false},
		{"on, in a focused drain", true, true, modeFocus, true},
		{"on, but not in triage", true, false, modeFocus, false},
		{"on, but back on the list", true, true, modeList, false},
	} {
		m.autoProceed, m.triage, m.mode = c.auto, c.triage, c.mode
		if got := m.autoProceeds(); got != c.want {
			t.Fatalf("%s: autoProceeds = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAutoProceedDefaultsOnAndPersists(t *testing.T) {
	m := buildModel(t)
	if !storedAutoProceed(m.store) {
		t.Fatal("a fresh store did not come up with auto proceed on")
	}
	if !m.autoProceed {
		t.Fatal("a fresh model came up with auto proceed off")
	}
	m.openSettings()
	m.settings.field = settingsFieldAutoProceed
	m.cycleSetting(1)
	m.saveAndCloseSettings()
	if m.autoProceed {
		t.Fatal("the model did not pick up the new setting")
	}
	if storedAutoProceed(m.store) {
		t.Fatalf("auto proceed off was not persisted: %s", m.errBar.text)
	}
	m.openSettings()
	m.settings.field = settingsFieldAutoProceed
	m.cycleSetting(-1)
	m.saveAndCloseSettings()
	if !m.autoProceed || !storedAutoProceed(m.store) {
		t.Fatal("auto proceed did not go back on")
	}
}

// An install that predates the on-by-default flip carries an explicit "off"
// that nobody chose: saveSettings writes every field on every save, so
// changing the theme once was enough to write it. The flip has to reach that
// store, and has to reach it exactly once, so an off chosen afterwards stays.
func TestAutoProceedFlipsAStoredOffOnceAndThenLeavesItAlone(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting(autoProceedDefaultSetting, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetSetting(autoProceedSetting, "off"); err != nil {
		t.Fatal(err)
	}
	if !storedAutoProceed(m.store) {
		t.Fatal("the flip did not reach a store carrying an incidental off")
	}
	if err := m.store.SetSetting(autoProceedSetting, "off"); err != nil {
		t.Fatal(err)
	}
	if storedAutoProceed(m.store) {
		t.Fatal("the flip ran twice and overrode an off the operator chose")
	}
}

// stageDialog poses the focused pane as a session stopped on a permission
// dialog, caret on the marker, using the test config's hooked tool -- the one
// carrying a prompt cutoff and a waiting rule to read it with.
func stageDialog(m *Model, sessID string) {
	for i := range m.sessions {
		m.sessions[i].Tool = "claude-hooked"
	}
	m.rebuildRows()
	m.preview = strings.Join([]string{
		"  Do you want to proceed?",
		"❯ 1. Yes",
		"  2. No",
		"",
		"  Enter to confirm · Esc to cancel",
	}, "\n") + "\n"
	m.pane.forID = sessID
	m.pane.box.height, m.pane.box.width = 5, 80
	m.pane.cursor = paneCursor{x: 0, y: 1, ok: true}
}

// The point of the setting: answering the dialog is the whole gesture, and
// the next session that needs somebody comes up by itself.
func TestAutoProceedHandsOverOnceTheDialogIsAnswered(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.triage = true
	m.autoProceed = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	stageDialog(m, focusedID(t, m))
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("answering dropped out of the queue: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("after answering, focused %q want %q", got, "next")
	}
}

// With the setting turned off the key goes to the pane and the operator stays
// where they are. Off is no longer the default, so the test says so itself
// rather than leaning on a fresh model being off.
func TestWithoutAutoProceedAnsweringStaysPut(t *testing.T) {
	m := buildModel(t)
	m.autoProceed = false
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	stageDialog(m, focusedID(t, m))
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("without the setting the answer moved to %q", got)
	}
}

// A key on the way to an answer is not one: typing into the dialog leaves the
// operator in the session, whatever the setting says.
func TestAutoProceedStaysPutOnAKeyThatIsNotAnAnswer(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.triage = true
	m.autoProceed = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	stageDialog(m, focusedID(t, m))
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(*Model)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("a plain keystroke handed the session over to %q", got)
	}
}

// The key that pulls a scrolled-back pane down to its live bottom is not an
// answer, whatever the row under the caret says: the frame on screen was
// history. Reading it after the pull -- which clears the scroll -- would make
// every such key look like one.
func TestAutoProceedStaysPutOnAScrolledBackPane(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.triage = true
	m.autoProceed = true
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	stageDialog(m, focusedID(t, m))
	m.focusScroll = 3
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("a key read off a scrolled-back frame handed the session over to %q", got)
	}
}

func focusedID(t *testing.T, m *Model) string {
	t.Helper()
	sess, ok := m.selected()
	if !ok {
		t.Fatal("nothing is selected")
	}
	return sess.ID
}

// A dialog with a question stepper is not answered by the first Enter. On a
// live dialog, Enter on a single-select question picks the row and moves the
// stepper to the next question; on a multi-select one it ticks the row's box
// and stays; only the review page under the stepper's Submit entry closes the
// dialog. Handing the session over from a question left the operator's
// remaining answers unanswered in a pane they had already left.
//
// The frames are live captures with their escapes, since the stepper marks
// its active entry by background colour alone. Each was drawn on a 319x78
// pane with the caret on the selected row's marker.
func TestAutoProceedAnswersAStepperDialogOnlyFromItsReviewPage(t *testing.T) {
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	one := tea.KeyPressMsg{Code: '1', Text: "1"}
	for _, c := range []struct {
		name   string
		file   string
		caretY int
		key    tea.KeyPressMsg
		want   bool
	}{
		// One multi-select question: the stepper is up with the question
		// active, and Enter ticks the highlighted box.
		{"enter on a multi-select question", "claude-multiselect-question", 15, enter, false},
		{"a row's number on a multi-select question", "claude-multiselect-question", 15, one, false},
		// The second of two questions, reached by answering the first.
		{"enter on a later question", "claude-stepper-second-question", 27, enter, false},
		// The review page: Submit is the active entry, and its numbered
		// list is the answer.
		{"enter on the review page", "claude-stepper-submit-review", 35, enter, true},
		{"a row's number on the review page", "claude-stepper-submit-review", 35, one, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", c.file+".txt"))
			if err != nil {
				t.Fatalf("read frame: %v", err)
			}
			m := &Model{engine: liveEngine(t), mode: modeFocus}
			m.preview = string(raw)
			m.pane.forID = "s1"
			m.pane.box.height, m.pane.box.width = 78, 319
			m.pane.cursor = paneCursor{x: 0, y: c.caretY, ok: true}
			sess := store.Session{ID: "s1", Tool: "claude"}
			// Every frame is a dialog to the board; what differs is where on
			// the stepper it is, so that is the only thing the verdict may
			// turn on.
			if !m.selectionDialogUp(sess.ID, sess.Tool) {
				t.Fatal("the frame does not read as a selection dialog, so this case is not testing the stepper")
			}
			if got := m.answersFocused(sess, c.key); got != c.want {
				if c.want {
					t.Fatal("the review page's answer did not hand the session over")
				}
				t.Fatal("a step along the dialog handed the session over before it was answered")
			}
		})
	}
}
