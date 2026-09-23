package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/snippets"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func gateKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'G', Text: "G", Mod: tea.ModShift} }

func altDot() tea.KeyPressMsg { return tea.KeyPressMsg{Code: '.', Text: ".", Mod: tea.ModAlt} }

func pressGate(t *testing.T, m *Model) *Model {
	t.Helper()
	updated, _ := m.handleKey(gateKey())
	return updated.(*Model)
}

func pressFocused(t *testing.T, m *Model, key tea.KeyPressMsg) *Model {
	t.Helper()
	updated, _ := m.handleFocusKey(key)
	return updated.(*Model)
}

// gateFleet is two sessions waiting, so there is a head to enter and a
// second one for the drain to promote.
func gateFleet(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"next": status.Waiting,
	})
	m.rebuildRows()
	return m
}

// One key, three arrangements: the queue, the whole width, and the session
// at the head of it open rather than a list to press enter on.
func TestGateArmsTheQueueTheWidthAndTheHeadSession(t *testing.T) {
	m := gateFleet(t)

	m = pressGate(t, m)

	if !m.gate.on {
		t.Fatalf("the gate did not arm: %s", m.errBar.text)
	}
	if !m.triage {
		t.Fatal("the gate armed without the queue under it")
	}
	if m.layout != layoutBoard {
		t.Fatalf("the rail kept its columns: layout %q", m.layout)
	}
	if m.mode != modeFocus {
		t.Fatalf("the gate stopped on the list rather than entering the head: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("the gate opened %q, want the session waiting longest", got)
	}
}

// Leaving puts back what arming changed, the operator's own layout included:
// somebody who chose mobile gets mobile, not the board the mode borrowed.
func TestGatePutsBackTheLayoutAndQueueItFound(t *testing.T) {
	m := gateFleet(t)
	m.layout = layoutMobile

	m = pressGate(t, m)
	if m.layout != layoutBoard {
		t.Fatalf("arming did not take the width: layout %q", m.layout)
	}

	m = pressFocused(t, m, ctrlBackslash())

	if m.gate.on {
		t.Fatal("ctrl+\\ left the session but not the gate")
	}
	if m.layout != layoutMobile {
		t.Fatalf("the gate handed back layout %q, want the one it found", m.layout)
	}
	if m.triage {
		t.Fatal("the queue the gate turned on outlived it")
	}
	if m.triageScope != "" {
		t.Fatalf("the scope the gate set outlived it: %q", m.triageScope)
	}
	if m.mode == modeFocus {
		t.Fatal("ctrl+\\ stayed inside the session")
	}
}

// The mode is the setting armed on purpose, so an answer promotes the next
// session without the operator having turned anything on in settings first.
func TestGateAdvancesOnAnAnswerWithoutTheSetting(t *testing.T) {
	m := gateFleet(t)
	m.autoProceed = false
	m = pressGate(t, m)
	if m.autoProceed {
		t.Fatal("arming the gate turned the setting on, so this proves nothing")
	}

	stageDialog(m, focusedID(t, m))
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.mode != modeFocus {
		t.Fatalf("answering dropped out of the drain: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("after answering, focused %q want %q", got, "next")
	}
}

// The same answer outside the gate, to show the advance is the mode and not
// something the fixture was doing anyway.
func TestWithoutTheGateTheSameAnswerStaysPut(t *testing.T) {
	m := gateFleet(t)
	m.autoProceed = false
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")

	stageDialog(m, focusedID(t, m))
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("an answer moved to %q with no gate armed", got)
	}
}

// The skip: a session that wants nothing is taken off the queue from inside
// it, without a trip back to a list the gate is not drawing.
func TestGateDismissSkipsToTheNext(t *testing.T) {
	m := gateFleet(t)
	m = pressGate(t, m)

	m = pressFocused(t, m, altDot())

	if m.mode != modeFocus {
		t.Fatalf("the skip dropped out of the drain: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("the skip landed on %q, want the next in the queue", got)
	}
}

// A drain that has run dry is the drain being over, so the last handover
// ends the mode and hands the board back rather than leaving it armed over
// an empty queue.
func TestGateEndsWhenTheQueueRunsDry(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	prior := m.layout
	m.rebuildRows()

	m = pressGate(t, m)
	if m.mode != modeFocus {
		t.Fatalf("the gate entered nothing: %s", m.errBar.text)
	}

	m = pressFocused(t, m, altDot())

	if m.gate.on {
		t.Fatal("the gate stayed armed with nothing left to drain")
	}
	if m.layout != prior {
		t.Fatalf("the drained gate left layout %q, want %q", m.layout, prior)
	}
	if m.mode == modeFocus {
		t.Fatal("the drained gate stayed inside a session")
	}
	if !strings.Contains(m.errBar.text, "drained") {
		t.Fatalf("the drained gate said %q", m.errBar.text)
	}
}

// alt+n starts a session and comes back: the spawn's landing is redirected to
// the session the gate was on, so a drain can gain a session without losing
// its place, which is what v1's gate view did.
func TestGateSpawnComesBackToTheGate(t *testing.T) {
	m := gateFleet(t)
	m.newSessionAgent = newSessionAgentLast
	m = pressGate(t, m)
	want := focusedID(t, m)
	before := len(m.sessions)

	m = pressFocused(t, m, jumpKey(t, "alt+n"))

	if len(m.sessions) != before+1 {
		t.Fatalf("alt+n made %d sessions, want %d", len(m.sessions), before+1)
	}
	if !m.gate.on {
		t.Fatal("the spawn left the gate")
	}
	if m.mode != modeFocus {
		t.Fatalf("the spawn left focus (mode %v, err %q)", m.mode, m.errBar.text)
	}
	if got := focusedID(t, m); got != want {
		t.Fatalf("the spawn landed on %q, want the gate's own session", got)
	}
}

// A spawn cancelled from the picker puts the operator back in the gate too:
// esc out of the flow must not also drop the drain.
func TestGateSpawnCancelReturnsToTheGate(t *testing.T) {
	m := gateFleet(t)
	m.newSessionAgent = newSessionAgentAsk
	m = pressGate(t, m)
	want := focusedID(t, m)

	m = pressFocused(t, m, jumpKey(t, "alt+n"))
	if m.mode != modeAgentPick {
		t.Fatalf("alt+n opened mode %v, want the agent picker", m.mode)
	}

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(*Model)

	if !m.gate.on {
		t.Fatal("cancelling the spawn left the gate")
	}
	if m.mode != modeFocus {
		t.Fatalf("cancelling the spawn left mode %v, want focus", m.mode)
	}
	if got := focusedID(t, m); got != want {
		t.Fatalf("cancelling the spawn landed on %q, want the gate's own session", got)
	}
}

// alt+l is the step back: reopening the session the drain skipped also lifts
// its mute, so it is on the queue again rather than only on screen.
func TestGateBackReopensTheSessionItLeft(t *testing.T) {
	m := gateFleet(t)
	m = pressGate(t, m)
	first := focusedID(t, m)

	m = pressFocused(t, m, altDot())
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("skip landed on %q, want next", got)
	}
	left := gateSession(t, m, first)
	if !m.isMuted(left) {
		t.Fatal("the skip did not mute the session it left")
	}

	m = pressFocused(t, m, jumpKey(t, "alt+l"))

	if got := focusedID(t, m); got != first {
		t.Fatalf("back landed on %q, want the session it left", got)
	}
	if !m.gate.on {
		t.Fatal("back left the gate")
	}
	if m.isMuted(left) {
		t.Fatal("back left the session muted")
	}
}

func gateSession(t *testing.T, m *Model, id string) store.Session {
	t.Helper()
	for _, sess := range m.sessions {
		if sess.ID == id {
			return sess
		}
	}
	t.Fatalf("no session %q on the board", id)
	return store.Session{}
}

func TestGateMenuTogglePersistsAcrossSessions(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	if !m.gate.menu {
		t.Fatal("gate should start in conversation mode")
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: '.', Text: "."})
	if got := focusedName(t, m); got != "next" || !m.gate.menu {
		t.Fatalf("skip lost menu mode or failed to advance: %q, menu=%v", got, m.gate.menu)
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: 'l', Text: "l"})
	if got := focusedName(t, m); got != "ask" || !m.gate.menu {
		t.Fatalf("back lost menu mode or failed to return: %q, menu=%v", got, m.gate.menu)
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	m = pressFocused(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.gate.menu || !m.gate.on || m.mode != modeFocus {
		t.Fatal("typing q acted as a menu command")
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	m = pressFocused(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})
	if m.gate.on || m.mode == modeFocus {
		t.Fatal("menu q did not exit the gate")
	}
}

func TestGateConversationDoesNotAnswerHiddenNativeDialogs(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	stageDialog(m, focusedID(t, m))
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := focusedName(t, m); got != "ask" || !m.gate.menu {
		t.Fatalf("conversation sent an unseen answer: %q, menu=%v", got, m.gate.menu)
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("terminal answer did not advance: %q", got)
	}
}

func TestGateMenuHomeJumpsToOldestHistory(t *testing.T) {
	m, _ := focusedWithHistory(t, "gate-top")
	m.conversation = nil
	m.gate.on, m.gate.menu = true, true
	_, cmd := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyHome})
	m.applyCmd(t, cmd)
	if m.focusScroll != m.pane.history || m.focusScroll == 0 {
		t.Fatalf("Home left scroll at %d, want history %d", m.focusScroll, m.pane.history)
	}
	_, cmd = m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.applyCmd(t, cmd)
	if m.scrolledBack() {
		t.Fatal("End did not return to live output")
	}
}

func TestGateToggleAndTopRemainVisibleAboveThePane(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	for _, width := range []int{40, 80, 120} {
		for _, menu := range []bool{false, true} {
			m.width, m.gate.menu = width, menu
			m.chrome = chromeNever
			top := strings.Split(m.frame(), "\n")[0]
			mode := "Terminal"
			if menu {
				mode = "Messages"
			}
			if !strings.Contains(top, mode) || !strings.Contains(top, "f2") || !strings.Contains(top, "top") {
				t.Fatalf("width %d, menu %v: missing top controls: %s", width, menu, top)
			}
		}
	}
}

// alt+y puts the agent's own conversation id on the clipboard, not the row id.
func TestGateCopySessionIDWritesTheConversationID(t *testing.T) {
	m := gateFleet(t)
	m = pressGate(t, m)
	sess, _ := m.selected()
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].AgentSessionID = "conv-abc-123"
		}
	}
	m.rebuildRows()

	orig := writeClipboard
	defer func() { writeClipboard = orig }()
	var copied string
	writeClipboard = func(id string) error {
		copied = id
		return nil
	}

	updated, cmd := m.handleFocusKey(jumpKey(t, "alt+y"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}

	if copied != "conv-abc-123" {
		t.Fatalf("clipboard got %q, want the conversation id", copied)
	}
	if !strings.Contains(m.errBar.text, "copied session id") {
		t.Fatalf("the status line said %q", m.errBar.text)
	}
}

// The gate's footer names the session controls v1's gate view carried, not
// only the drain's three gestures.
func TestGateFooterNamesTheSessionControls(t *testing.T) {
	m := gateFleet(t)
	m = pressGate(t, m)

	footer := m.viewFooter()
	for _, want := range []string{
		"reply", "skip", "exit", "top", "bottom",
		"end", "new", "copy ID", "back",
	} {
		if !strings.Contains(footer, want) {
			t.Errorf("gate footer is missing %q:\n%s", want, footer)
		}
	}
}

// The operator's snippets answer the session in front of you, which is what
// the gate is for, so the drain's tier carries them too rather than leaving
// the key map as the only place a chord is named.
func TestGateFooterNamesTheSnippets(t *testing.T) {
	m := gateFleet(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "d", Label: "deploy", Text: "ship it now"}})
	m = pressGate(t, m)

	footer := ansi.Strip(m.viewFooter())
	// The chord is spelled the way this OS spells alt: ⌥ on a Mac, alt+
	// everywhere else, so the assertion follows keymap.Display rather than
	// pinning one platform's glyph.
	chord := "^" + keymap.Display("alt+d")
	if !strings.Contains(footer, chord) || !strings.Contains(footer, "deploy") {
		t.Fatalf("gate footer does not name the snippet %s:\n%s", chord, footer)
	}
}

// Arming over a queue with nothing in it stays armed and says so: disarming
// here would look exactly like a key that does nothing. The second press is
// the way out, and the badge is where it is named -- with the rail away
// there is no other row printing it.
func TestGateOnAnEmptyQueueSaysSoAndStopsOnTheSecondPress(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"busy": status.Working})
	prior := m.layout
	m.rebuildRows()

	m = pressGate(t, m)

	if m.mode == modeFocus {
		t.Fatal("nothing was waiting, so the gate should have entered nothing")
	}
	if !m.gate.on {
		t.Fatal("an empty queue disarmed the gate instead of saying it was empty")
	}
	if !strings.Contains(m.errBar.text, "queue") {
		t.Fatalf("the empty gate said %q", m.errBar.text)
	}
	if rail := triageRail(m); !strings.Contains(rail, "GATE") {
		t.Fatalf("the gate did not name its own way out:\n%s", rail)
	}

	m = pressGate(t, m)

	if m.gate.on {
		t.Fatal("the second press left the gate armed")
	}
	if m.layout != prior {
		t.Fatalf("the second press left layout %q, want %q", m.layout, prior)
	}
	if m.triage {
		t.Fatal("the second press left the queue on")
	}
}

// Both ways out of a drain rebuild the list the gate flattened, and the
// session the drain ended on can come back inside a group the operator had
// folded. The cursor lands on that group, so the pane still on screen belongs
// to a row nobody is on -- which reads as the restored view being broken.
// Checked on both exits because they reach disarmGate from different modes.
func TestGateExitClearsPreviewHiddenByRestoredGroup(t *testing.T) {
	for _, exit := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"stop", ctrlBackslash()},
		{"drain", altDot()},
	} {
		t.Run(exit.name, func(t *testing.T) {
			m := buildModel(t)
			if err := m.store.CreateGroup("work", ""); err != nil {
				t.Fatal(err)
			}
			loadStoredRows(t, m)
			createSession(t, m, "grouped", t.TempDir(), "work")
			sess := m.sessionRows()[0]
			if err := m.store.UpdateStatus(sess.ID, status.Waiting); err != nil {
				t.Fatal(err)
			}
			loadStoredRows(t, m)
			m.collapsed["work"] = true
			m.rebuildRows()
			m.selectGroupRow(t, "work")
			m = pressGate(t, m)
			if m.mode != modeFocus {
				t.Fatalf("gate did not focus grouped session: %s", m.errBar.text)
			}
			m.preview = "stale focused pane"
			m.pane.forID = sess.ID
			gen := m.previewGen

			m = pressFocused(t, m, exit.key)

			if m.mode != modeList || m.gate.on || m.triage {
				t.Fatalf("exit left mode=%v gate=%v triage=%v", m.mode, m.gate.on, m.triage)
			}
			if row, ok := m.selectedRow(); !ok || !row.isGroup {
				t.Fatal("exit did not return to the collapsed group")
			}
			if m.preview != "" || m.pane.forID != "" {
				t.Fatalf("restored group retained session preview %q (%q)", m.preview, m.pane.forID)
			}
			if m.previewGen <= gen {
				t.Fatal("exit still accepts the focused session's pending preview")
			}
		})
	}
}
