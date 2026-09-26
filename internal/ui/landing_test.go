package ui

import (
	"testing"

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
	stale := m.landing.gen
	m = pressEnter(m)
	m.applyLandingCheck(landingCheckMsg{gen: stale, verdict: landingSeen})
	if !m.landing.active || m.landing.gen == stale {
		t.Fatal("a look for the replaced key decided the pending answer")
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("a stale look handed the session over to %q", got)
	}
}
