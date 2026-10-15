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

// pressDialogKey answers the staged dialog with its option key, an answer
// the drain still waits to see land before moving on. Enter moves on with the
// key itself; see TestEnterHandsOverAtOnce.
func pressDialogKey(m *Model) *Model {
	updated, _ := m.handleFocusKey(tea.KeyPressMsg{Code: '1', Text: "1"})
	return updated.(*Model)
}

// Enter is the operator done with the session, so the drain moves on with the
// key rather than once the answer is seen landing. The check still runs, and
// records the answer when it lands without moving the drain a second time.
func TestEnterHandsOverAtOnce(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m = pressEnter(m)
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("Enter focused %q want %q", got, "next")
	}
	nextID := focusedID(t, m)
	if m.handoverTrace.to != nextID {
		t.Fatal("the handover is not timing the next session's first frame")
	}
	m.noteHandoverFrame(nextID)
	if m.handoverTrace.to != "" {
		t.Fatal("the handover frame was logged but its trace stayed armed")
	}
	recordDialogResult(t, m, askID, "answered")
	lookForLanding(t, m)
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("the answer landing moved the drain again, to %q", got)
	}
	if len(m.landings) != 0 {
		t.Fatal("the landing stayed pending after it was seen")
	}
	if m.latestSubmission.sessionID != askID {
		t.Fatal("a landed answer was not recorded as a submission")
	}
}

// The session an Enter hands over is captured at once rather than after the
// settle a cursor walking the rail waits out, and the pane left behind is not
// chased: the next pane's first frame is the one the operator reads.
func TestEnterCapturesTheNextPaneAtOnce(t *testing.T) {
	m := drainOnDialog(t, true)
	updated, cmd := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	nextID := focusedID(t, m)
	msgs := make(chan tea.Msg, 64)
	go func() { msgs <- cmd() }()
	deadline := time.After(previewSettle * 4)
	for {
		select {
		case msg := <-msgs:
			switch msg := msg.(type) {
			case tea.BatchMsg:
				for _, sub := range msg {
					if sub != nil {
						go func() { msgs <- sub() }()
					}
				}
			case previewSettleMsg:
				t.Fatal("the handover waited out the cursor's settle before capturing")
			case previewMsg:
				if msg.chase {
					t.Fatal("the handover chased the pane it left")
				}
				if msg.sessID == nextID {
					return
				}
			}
		case <-deadline:
			t.Fatal("no capture of the next pane arrived")
		}
	}
}

// An Enter that answered nothing hands its session back to the queue as soon
// as the check gives up on it, rather than a poll later.
func TestAnEnterThatLandsNothingReturnsToTheQueue(t *testing.T) {
	for _, refused := range []bool{true, false} {
		t.Run(map[bool]string{true: "refused", false: "timed out"}[refused], func(t *testing.T) {
			m := drainOnDialog(t, true)
			ask, _ := m.selected()
			m = pressEnter(m)
			if !m.isMuted(ask) {
				t.Fatal("the session handed over on Enter was not muted")
			}
			if refused {
				logHookEvent(t, m, ask.ID, "waiting Notification")
			} else {
				onlyLanding(t, m).deadline = time.Now().Add(-time.Millisecond)
			}
			lookForLanding(t, m)
			if len(m.landings) != 0 {
				t.Fatal("the landing outlived its verdict")
			}
			if m.isMuted(ask) {
				t.Fatal("an answer that never landed kept its session out of the queue")
			}
			if m.latestSubmission.sessionID != "" {
				t.Fatal("an answer that never landed was recorded as a submission")
			}
			if got := focusedName(t, m); got != "next" {
				t.Fatalf("giving up on the answer moved focus to %q", got)
			}
		})
	}
}

// The point of the setting: answering the dialog is the whole gesture, and
// the next session that needs somebody comes up by itself -- once the agent
// is seen to have taken the answer.
func TestAutoProceedHandsOverOnceTheAnswerLands(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m = pressDialogKey(m)
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
	m = pressDialogKey(m)
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
			m = pressDialogKey(m)
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
	m = pressDialogKey(m)
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

func TestSubmissionHonorsNextSessionOutsideTriage(t *testing.T) {
	for _, state := range []string{status.Idle, status.Waiting} {
		for _, tc := range []struct {
			name  string
			auto  bool
			leave string
			want  string
		}{
			{"advance enabled", true, leaveToNext, "next"},
			{"auto proceed disabled", false, leaveToNext, "answered"},
			{"return to list selected", true, leaveToList, "answered"},
		} {
			t.Run(state+"/"+tc.name, func(t *testing.T) {
				m := buildModel(t)
				m.autoProceed, m.leaveMode = tc.auto, tc.leave
				liveTriageFleet(t, m, map[string]string{
					"answered": state,
					"next":     status.Waiting,
					"busy":     status.Working,
				})
				m.rebuildRows()
				m.enterFocusOn(t, "answered")
				id := focusedID(t, m)
				stageDialog(m, id)
				if state == status.Idle {
					m.preview = "❯ go on\n"
					m.pane.cursor = paneCursor{x: 7, y: 0, ok: true}
				}
				if m.triage {
					t.Fatal("the fixture armed triage")
				}

				m = pressEnter(m)
				if got := focusedName(t, m); got != tc.want {
					t.Fatalf("Enter focused %q, want %q", got, tc.want)
				}
				if state == status.Waiting {
					recordDialogResult(t, m, id, "answered")
				} else {
					logHookEvent(t, m, id, "working UserPromptSubmit")
				}
				lookForLanding(t, m)
				if m.latestSubmission.sessionID != id {
					t.Fatal("the submitted answer was not accepted")
				}
				if m.mode != modeFocus || focusedName(t, m) != tc.want {
					t.Fatalf("after submission focused %q in mode %v, want %q", focusedName(t, m), m.mode, tc.want)
				}
				if m.triage {
					t.Fatal("submitting enabled triage")
				}
			})
		}
	}
}

func TestEarlyHandoverPreservesTheNextFinishedAlert(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"done": status.Finished, "next": status.Waiting})
	m.autoProceed, m.triage = true, true
	m.rebuildRows()
	m.enterFocusOn(t, "done")
	id := focusedID(t, m)
	if m.heldAckID != id {
		t.Fatal("the finished session did not hold its acknowledgement")
	}
	updated, cmd := m.handleFocusKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if focusedName(t, m) != "next" {
		t.Fatal("Enter did not hand over immediately")
	}
	for _, state := range []string{status.Working, status.Finished} {
		if err := m.store.UpdateStatus(id, state); err != nil {
			t.Fatal(err)
		}
	}
	runStoreCmd(t, cmd)
	if got := storedSession(t, m, "done"); got.Status != status.Finished || got.Acked {
		t.Fatalf("the next turn lost its alert: status %q acked %v", got.Status, got.Acked)
	}
}

func TestFailedEarlyHandoverResumesTheDrainedQueue(t *testing.T) {
	for _, refused := range []bool{true, false} {
		t.Run(map[bool]string{true: "refused", false: "timed out"}[refused], func(t *testing.T) {
			m := buildModel(t)
			liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
			m.autoProceed, m.triage = true, true
			m.rebuildRows()
			m.enterFocusOn(t, "ask")
			id := focusedID(t, m)
			stageDialog(m, id)
			m = pressEnter(m)
			if m.mode != modeList || !m.triageResume {
				t.Fatal("the lone session did not leave the drain open on the list")
			}
			pending := onlyLanding(t, m)
			verdict := landingPending
			if refused {
				verdict = landingRefused
			} else {
				pending.deadline = time.Now().Add(-time.Millisecond)
			}
			cmd := m.applyLandingCheck(landingCheckMsg{id: id, gen: pending.gen, verdict: verdict})
			if cmd == nil || m.mode != modeFocus || focusedID(t, m) != id {
				t.Fatal("the failed answer did not immediately reopen the lone session")
			}
			if m.triageResume || len(m.landings) != 0 || m.latestSubmission.sessionID != "" {
				t.Fatal("the failed answer left stale drain or submission state")
			}
		})
	}
}
