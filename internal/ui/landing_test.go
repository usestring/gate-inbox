package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
)

func TestHookVerdictReadsAComposerAndADialogDifferently(t *testing.T) {
	ev := func(state, name string) hooks.Event { return hooks.Event{State: state, Name: name} }
	for _, c := range []struct {
		name          string
		dialog        bool
		events        []hooks.Event
		seen, refused bool
	}{
		{"a prompt the agent took", false, []hooks.Event{ev(status.Working, "UserPromptSubmit")}, true, false},
		// /model and the other built-ins log no prompt, so the drain stays on
		// the picker they open.
		{"a built-in command", false, nil, false, false},
		{"a tool call is not a prompt", false, []hooks.Event{ev(status.Working, "PostToolUse")}, false, false},
		{"the allowed call ran", true, []hooks.Event{ev(status.Working, "PostToolUse")}, true, false},
		{"another dialog came up", true, []hooks.Event{ev(status.Waiting, "Notification"), ev(status.Working, "PostToolUse")}, false, true},
		{"the turn stopped without the call", true, []hooks.Event{ev(status.Finished, "Stop")}, false, true},
		{"the next question after this one landed", true, []hooks.Event{ev(status.Working, "PostToolUse"), ev(status.Waiting, "PreToolUse")}, true, false},
		{"the agent only started to work", true, []hooks.Event{ev(status.Working, "PreToolUse")}, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &landingProbe{dialog: c.dialog}
			seen, refused := p.hookVerdict(c.events)
			if seen != c.seen || refused != c.refused {
				t.Fatalf("hookVerdict = seen %v refused %v, want %v %v", seen, refused, c.seen, c.refused)
			}
		})
	}
}

func TestTranscriptVerdictReadsRefusalsBeforeLanding(t *testing.T) {
	for _, c := range []struct {
		name          string
		dialog        bool
		delta         convo.Delta
		seen, refused bool
	}{
		{"a typed prompt", false, convo.Delta{Prompts: []string{"go on"}}, true, false},
		{"a tool result is not a prompt", false, convo.Delta{Results: []convo.Result{{ToolUseID: "t1"}}}, false, false},
		{"a dialog answered", true, convo.Delta{Results: []convo.Result{{ToolUseID: "t1"}}}, true, false},
		{"a dialog declined", true, convo.Delta{Results: []convo.Result{{ToolUseID: "t1", Rejected: true}}}, false, true},
		{"a turn interrupted", false, convo.Delta{Prompts: []string{"[Request interrupted by user]", "go on"}}, false, true},
		{"nothing yet", true, convo.Delta{}, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &landingProbe{dialog: c.dialog}
			seen, refused := p.transcriptVerdict(c.delta)
			if seen != c.seen || refused != c.refused {
				t.Fatalf("transcriptVerdict = seen %v refused %v, want %v %v", seen, refused, c.seen, c.refused)
			}
		})
	}
}

// The pane is the fallback: it can land an answer the files have not reported
// yet, but never outvote a refusal they have.
func TestThePaneLandsOnlyWhatNoOtherSourceRefused(t *testing.T) {
	working := false
	p := &landingProbe{dialog: true, paneWorking: func() bool { return working }}
	if got := p.look(); got != landingPending {
		t.Fatalf("a pane still on its dialog read %v", got)
	}
	working = true
	if got := p.look(); got != landingSeen {
		t.Fatalf("a pane gone to work read %v", got)
	}

	m := buildModel(t)
	logHookEvent(t, m, "declined", "waiting Notification")
	refusing := &landingProbe{dialog: true, hooks: m.hooks, hookID: "declined", paneWorking: func() bool { return true }}
	if got := refusing.look(); got != landingRefused {
		t.Fatalf("a refusal in the hook log lost to the pane: %v", got)
	}
}

// A look for an older key must not decide the answer that replaced it.
func TestAStaleLookDecidesNothing(t *testing.T) {
	m := drainOnDialog(t, true)
	m = pressEnter(m)
	askID := focusedID(t, m)
	stale := onlyLanding(t, m).gen
	m = pressEnter(m)
	m.applyLandingCheck(landingCheckMsg{id: askID, gen: stale, verdict: landingSeen})
	if pending := onlyLanding(t, m); pending.gen == stale {
		t.Fatal("a look for the replaced key decided the pending answer")
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("a stale look handed the session over to %q", got)
	}
}

// A quick prompt that is never seen landing records nothing and moves the
// drain nowhere.
func TestAQuickPromptThatNeverLandsStaysPut(t *testing.T) {
	m := enterDrain(t, drainFleet(t))
	sess, _ := m.selected()
	m.openQuickMode()
	m.quick.input.SetValue("Please continue")
	if _, _ = m.submitQuick(); m.errBar.text != "" {
		t.Fatal(m.errBar.text)
	}
	m.landings[sess.ID].deadline = time.Now().Add(-time.Millisecond)
	lookForLanding(t, m)
	if len(m.landings) != 0 {
		t.Fatal("the prompt outlived its window")
	}
	if focusedID(t, m) != sess.ID {
		t.Fatal("a prompt that never landed handed the session over")
	}
	if m.latestSubmission.sessionID != "" {
		t.Fatal("a prompt that never landed was made rescindable")
	}
}

// Snippets sent down the list to one row after another are each waited on,
// so the first is still reported when the second goes out inside its window.
func TestSnippetsToSeveralRowsEachLand(t *testing.T) {
	m := buildModel(t)
	bindPlusMinus(t, m)
	liveTriageFleet(t, m, map[string]string{"one": status.Waiting, "two": status.Waiting})
	m.rebuildRows()
	observer := &recordingObserver{}
	m.ObserveBoard(observer)

	one, two := sessionNamed(t, m, "one"), sessionNamed(t, m, "two")
	m.selectSessionRow(t, "one")
	sendPlusMinusToSelected(t, m)
	m.selectSessionRow(t, "two")
	sendPlusMinusToSelected(t, m)
	if len(m.landings) != 2 {
		t.Fatalf("%d snippets pending, want both", len(m.landings))
	}
	landAnswer(t, m, one.ID)
	landAnswer(t, m, two.ID)
	if got := operatorInputs(observer); len(got) != 2 {
		t.Fatalf("reported %+v, want both snippets", got)
	}
}

// Undo withdraws an answer the drain had counted as dealt with, so the
// session is the operator's again: its handover mute goes with it.
func TestRescindingALandedAnswerUnmutesItsSession(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m = pressEnter(m)
	logHookEvent(t, m, askID, "working PostToolUse")
	lookForLanding(t, m)
	ask, _ := m.sessionByID(askID)
	if !m.isMuted(ask) {
		t.Fatal("the handover did not mute the answered session, so this proves nothing")
	}
	m.rescindLatestSubmission()
	if !strings.Contains(m.errBar.text, "undid") {
		t.Fatalf("rescind result = %q", m.errBar.text)
	}
	if m.isMuted(ask) {
		t.Fatal("the rescinded session stayed muted out of the queue")
	}
}

// An answer landing on a finished session starts a new turn, so the handover
// that follows must not spend the held acknowledgement on the turn it
// answered: that would swallow the alert before any poll saw the new turn.
func TestALandedAnswerLetsGoOfTheHeldAck(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"done": status.Finished, "next": status.Waiting})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "done")
	id := focusedID(t, m)
	if m.heldAckID != id {
		t.Fatalf("entering held %q, want %q", m.heldAckID, id)
	}

	m = pressEnter(m)
	runStoreCmd(t, landAnswer(t, m, id))
	if got := focusedName(t, m); got == "done" {
		t.Fatal("the landed answer did not hand the session over, so this proves nothing")
	}
	if got := storedSession(t, m, "done"); got.Acked {
		t.Fatal("handing over after a landed answer spent the held acknowledgement")
	}
}
