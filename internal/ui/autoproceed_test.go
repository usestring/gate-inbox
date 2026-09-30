package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
)

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

// drainOnDialog is a triage drain with auto-proceed on, focused on "ask"
// stopped at a permission dialog, with "next" waiting behind it.
func drainOnDialog(t *testing.T, auto bool) *Model {
	t.Helper()
	m := buildModel(t)
	m.autoProceed = auto
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	stageDialog(m, focusedID(t, m))
	return m
}

// logHookEvent appends one line to a session's hook log, as its hooks would.
func logHookEvent(t *testing.T, m *Model, id, line string) {
	t.Helper()
	path := m.hooks.StatusFile(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// lookForLanding runs one look for the pending answer and applies it, which
// is what the tick does off the event loop.
func lookForLanding(t *testing.T, m *Model) {
	t.Helper()
	pending := onlyLanding(t, m)
	m.applyLandingCheck(landingCheckMsg{id: pending.sess.ID, gen: pending.gen, verdict: pending.probe.look()})
}

// landAnswer decides the answer pending for id as seen, for the tests about
// what landing does rather than how it is seen; landing_test.go covers that.
func landAnswer(t *testing.T, m *Model, id string) tea.Cmd {
	t.Helper()
	pending, ok := m.landings[id]
	if !ok {
		t.Fatalf("no answer pending for %s", id)
	}
	return m.applyLandingCheck(landingCheckMsg{id: id, gen: pending.gen, verdict: landingSeen})
}

// onlyLanding is the one answer pending on the board.
func onlyLanding(t *testing.T, m *Model) *pendingLanding {
	t.Helper()
	if len(m.landings) != 1 {
		t.Fatalf("%d answers pending, want one", len(m.landings))
	}
	for _, pending := range m.landings {
		return pending
	}
	return nil
}

func pressEnter(m *Model) *Model {
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	return updated.(*Model)
}

// The point of the setting: answering the dialog is the whole gesture, and
// the next session that needs somebody comes up by itself -- once the agent
// is seen to have taken the answer.
func TestAutoProceedHandsOverOnceTheAnswerLands(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m = pressEnter(m)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("the key alone handed the session over to %q", got)
	}
	recordDialogResult(t, m, askID, "answered")
	lookForLanding(t, m)
	if m.mode != modeFocus {
		t.Fatalf("answering dropped out of the queue: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("after the answer landed, focused %q want %q", got, "next")
	}
	if len(m.landings) != 0 {
		t.Fatal("the landing stayed pending after it was seen")
	}
	if m.latestSubmission.sessionID != askID {
		t.Fatal("a landed answer was not recorded as a submission")
	}
}

// Nothing seen inside the window is the key not having answered anything,
// and the operator stays where they are.
func TestAutoProceedStaysWhenNothingLands(t *testing.T) {
	m := drainOnDialog(t, true)
	m = pressEnter(m)
	lookForLanding(t, m)
	if len(m.landings) == 0 {
		t.Fatal("a look with nothing to see gave up before the window closed")
	}
	onlyLanding(t, m).deadline = time.Now().Add(-time.Millisecond)
	lookForLanding(t, m)
	if len(m.landings) != 0 {
		t.Fatal("the landing outlived its window")
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("an answer that never landed handed the session over to %q", got)
	}
	if m.latestSubmission.sessionID != "" {
		t.Fatal("an answer that never landed was recorded as a submission")
	}
}

// A dialog that gives way to another dialog, or a turn that stops without the
// call, is the answer not having gone through: the operator is still needed.
func TestAutoProceedStaysOnARefusalOrANewDialog(t *testing.T) {
	for _, line := range []string{"waiting Notification", "waiting PreToolUse", "finished Stop"} {
		t.Run(line, func(t *testing.T) {
			m := drainOnDialog(t, true)
			askID := focusedID(t, m)
			m = pressEnter(m)
			logHookEvent(t, m, askID, line)
			logHookEvent(t, m, askID, "working PostToolUse")
			lookForLanding(t, m)
			if len(m.landings) != 0 {
				t.Fatal("a refusal left the landing pending")
			}
			if got := focusedName(t, m); got != "ask" {
				t.Fatalf("a refused answer handed the session over to %q", got)
			}
		})
	}
}

// With the setting turned off the answer still lands, and the operator stays
// where they are. Off is no longer the default, so the test says so itself
// rather than leaning on a fresh model being off.
func TestWithoutAutoProceedAnsweringStaysPut(t *testing.T) {
	m := drainOnDialog(t, false)
	askID := focusedID(t, m)
	m = pressEnter(m)
	recordDialogResult(t, m, askID, "answered")
	lookForLanding(t, m)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("without the setting the answer moved to %q", got)
	}
	if m.latestSubmission.sessionID != askID {
		t.Fatal("the landed answer was not recorded without the setting")
	}
}

// A key after the answer is the operator still at work in the session, so
// the answer landing no longer carries them off it.
func TestAutoProceedStaysWhenTheOperatorKeepsTyping(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m = pressEnter(m)
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(*Model)
	recordDialogResult(t, m, askID, "answered")
	lookForLanding(t, m)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("an answer the operator typed on past handed the session over to %q", got)
	}
}

// Shift+enter is how a message takes a newline, so it arms nothing.
func TestAutoProceedArmsNothingOnAModifiedEnter(t *testing.T) {
	m := drainOnDialog(t, true)
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	m = updated.(*Model)
	if len(m.landings) != 0 {
		t.Fatal("shift+enter armed a landing")
	}
}

// The key that pulls a scrolled-back pane down to its live bottom cannot be
// read as answering the dialog on screen: the frame was history. It arms as
// a composer key, which a tool call finishing does not land.
func TestAutoProceedStaysPutOnAScrolledBackPane(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m.focusScroll = 3
	m = pressEnter(m)
	logHookEvent(t, m, askID, "working PostToolUse")
	lookForLanding(t, m)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("a key read off a scrolled-back frame handed the session over to %q", got)
	}
}

// A mute keyed to the landing holds until a poll listed after it has been
// applied, and then lapses whatever the session reads: the poll is the
// session's own news, and a mute that outlived it would hide the session for
// the rest of the drain.
func TestLandedMuteLapsesWithTheFirstPollAfterIt(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m = pressEnter(m)
	recordDialogResult(t, m, askID, "answered")
	lookForLanding(t, m)
	ask, ok := m.sessionByID(askID)
	if !ok {
		t.Fatal("ask left the board")
	}
	mark := m.muted[askID]
	if mark.settle.IsZero() {
		t.Fatal("the handover's mute was not keyed to the landing")
	}
	updated, _ := m.Update(refreshMsg{sessions: m.sessions, listedAt: mark.settle.Add(-time.Millisecond)})
	m = updated.(*Model)
	if !m.isMuted(ask) {
		t.Fatal("a poll listed before the landing lifted its mute")
	}
	updated, _ = m.Update(refreshMsg{sessions: m.sessions, listedAt: mark.settle.Add(time.Millisecond)})
	m = updated.(*Model)
	if m.isMuted(ask) {
		t.Fatal("the mute outlived the first poll after the landing, though the session still reads waiting")
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
