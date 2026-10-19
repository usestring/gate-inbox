package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/codexq"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestEnterStartsLookingWithoutATimer(t *testing.T) {
	before := landingTick
	landingTick = time.Hour
	t.Cleanup(func() { landingTick = before })
	m := &Model{}
	cmd := m.armLanding(store.Session{ID: "row"}, time.Now(), extension.OperatorPane, "", false, true, "❯ ")
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case msg := <-result:
		if got, ok := msg.(landingCheckMsg); !ok || got.id != "row" || got.verdict != landingPending {
			t.Fatalf("first look = %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter delayed its first acceptance check")
	}
}

func TestStaleRolloutStillChecksOnlyTheAnsweredPane(t *testing.T) {
	m := buildModel(t)
	tool := m.cfg.Tools["claude"]
	tool.SessionStore = search.ToolCodex
	m.cfg.Tools["claude"] = tool
	engine, err := status.NewEngine(config.Config{Tools: map[string]config.Tool{
		"claude": {DefaultStatus: status.Idle, Rules: []config.Rule{{State: status.Working, Pattern: "WORKING-NOW"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m.engine = engine
	liveTriageFleet(t, m, map[string]string{"answer": status.Idle, "other": status.Idle})
	m.enterFocusOn(t, "answer")
	sess, _ := m.selected()
	sess.AgentSessionID = "stale"
	root := t.TempDir()
	path := filepath.Join(root, "rollout-stale.jsonl")
	appendLandingRecord(t, path, `{"payload":{"type":"message","role":"user","content":[]}}`)
	m.landingLocator = search.NewLocator("", root)
	p := m.landingProbeFor(sess, false, m.echoBaseline(sess.ID))
	if p.rollout != path || p.paneWorking == nil {
		t.Fatal("a bound rollout disabled the current pane fallback")
	}
	sess.TmuxSocket, sess.TmuxPaneID = "default", "%1"
	adopted := m.landingProbeFor(sess, false, m.echoBaseline(sess.ID))
	if adopted.rollout != "" || adopted.paneWorking == nil {
		t.Fatal("an adopted Codex pane trusted an unverified conversation binding")
	}
	for _, row := range m.sessions {
		if row.ID != sess.ID {
			if err := m.tmux.SendText(row.ID, "WORKING-NOW"); err != nil {
				t.Fatal(err)
			}
			waitForPaneText(t, m, row.ID, "WORKING-NOW")
		}
	}
	if got := p.look(); got != landingPending {
		t.Fatalf("another pane landed this answer: %v", got)
	}
	appendLandingRecord(t, path, `{"payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"unrelated answer"}]}}`)
	if got := adopted.look(); got != landingPending {
		t.Fatalf("the stale rollout landed the adopted pane's answer: %v", got)
	}
	if err := m.tmux.SendText(sess.ID, "WORKING-NOW"); err != nil {
		t.Fatal(err)
	}
	waitForPaneText(t, m, sess.ID, "WORKING-NOW")
	if got := adopted.look(); got != landingSeen {
		t.Fatalf("the current pane's transition did not land: %v", got)
	}
}

func TestEnterDuringAnEchoChaseSnapshotsWorkAlreadyOnThePane(t *testing.T) {
	m := buildModel(t)
	engine, err := status.NewEngine(config.Config{Tools: map[string]config.Tool{
		"claude": {DefaultStatus: status.Idle, Rules: []config.Rule{{State: status.Working, Pattern: "WORKING-NOW"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m.engine = engine
	liveTriageFleet(t, m, map[string]string{"answer": status.Idle})
	m.enterFocusOn(t, "answer")
	id := focusedID(t, m)
	if err := m.tmux.SendText(id, "WORKING-NOW"); err != nil {
		t.Fatal(err)
	}
	waitForPaneText(t, m, id, "WORKING-NOW")
	m.preview = "❯ "
	m.focusChasing = true
	m = pressEnter(m)
	if onlyLanding(t, m).probe.paneWorking != nil {
		t.Fatal("Enter trusted the stale preview instead of the fresh working pane")
	}
}

func recordDialogResult(t *testing.T, m *Model, id, callID string) {
	t.Helper()
	p := m.landings[id].probe
	if p.transcript == "" {
		p.transcript = filepath.Join(t.TempDir(), "transcript.jsonl")
		p.toolID = "answered"
	}
	appendLandingRecord(t, p.transcript, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"`+callID+`","content":"ok"}]}}`)
}

func appendLandingRecord(t *testing.T, path, record string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(record + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundActivityCannotHandOverAnUnansweredDialog(t *testing.T) {
	m := drainOnDialog(t, true)
	id := focusedID(t, m)
	m = pressDialogKey(m)
	logHookEvent(t, m, id, "working PostToolUse")
	recordDialogResult(t, m, id, "background")
	lookForLanding(t, m)
	if focusedName(t, m) != "ask" || m.latestSubmission.sessionID != "" || len(m.landings) != 1 {
		t.Fatal("background activity handed over or recorded the unanswered dialog")
	}
	recordDialogResult(t, m, id, "answered")
	logHookEvent(t, m, id, "waiting PreToolUse")
	lookForLanding(t, m)
	if focusedName(t, m) != "next" || m.latestSubmission.sessionID != id {
		t.Fatal("the matching answer did not land before the next question")
	}
}

func TestDialogNeedsAKnownTargetToTrustATranscriptResult(t *testing.T) {
	p := &landingProbe{dialog: true}
	if seen, refused := p.transcriptVerdict(convo.Delta{Results: []convo.Result{{ToolUseID: "unknown"}}}); seen || refused {
		t.Fatal("a result identified a dialog whose target is unknown")
	}
}

func TestCodexLandingRequiresTheOutstandingCallID(t *testing.T) {
	for _, tc := range []struct {
		name, id, answers string
		want              landingVerdict
	}{
		{"another question", "other", `{"choice":"yes"}`, landingPending},
		{"auto-expired question", "ask", `{}`, landingRefused},
		{"matching question", "ask", `{"choice":"yes"}`, landingSeen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			appendLandingRecord(t, path, `{"payload":{"type":"function_call","name":"request_user_input","call_id":"ask","arguments":"{}"}}`)
			tracker := &codexq.Tracker{}
			if _, err := tracker.Update(path); err != nil {
				t.Fatal(err)
			}
			p := &landingProbe{dialog: true, toolID: "ask", rollout: path, rolloutQuestions: tracker}
			appendLandingRecord(t, path, `{"payload":{"type":"function_call_output","call_id":"`+tc.id+`","output":{"answers":`+tc.answers+`}}}`)
			if tc.want == landingRefused {
				p.paneWorking = func() bool { return true }
			}
			if got := p.look(); got != tc.want {
				t.Fatalf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLandingSnapshotSelectsTheDialogCall(t *testing.T) {
	m := buildModel(t)
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "project")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "conversation.jsonl")
	appendLandingRecord(t, path, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"background","name":"Bash"},{"type":"tool_use","id":"ask","name":"AskUserQuestion"}]}}`)
	m.landingLocator = search.NewLocator(root, "")
	m.preview = askPane
	p := m.landingProbeFor(store.Session{ID: "row", Tool: "claude", AgentSessionID: "conversation"}, true, m.preview)
	if p.toolID != "ask" {
		t.Fatalf("target = %q, want ask", p.toolID)
	}
	appendLandingRecord(t, path, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"background","content":"ok"}]}}`)
	if got := p.look(); got != landingPending {
		t.Fatalf("background result = %v", got)
	}
	appendLandingRecord(t, path, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"ask","content":"yes"}]}}`)
	if got := p.look(); got != landingSeen {
		t.Fatalf("matching result = %v", got)
	}
}

func TestAReplacedLandingSourceCannotReplayOldCompletion(t *testing.T) {
	for _, source := range []string{"transcript", "rollout"} {
		t.Run(source, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.jsonl")
			appendLandingRecord(t, path, "original")
			before, _ := os.Stat(path)
			p := &landingProbe{dialog: true, toolID: "ask", paneWorking: func() bool { return true }}
			if source == "transcript" {
				p.transcript, p.transcriptFile = path, before
			} else {
				p.rollout, p.rolloutFile = path, before
			}
			if err := os.Rename(path, path+".old"); err != nil {
				t.Fatal(err)
			}
			appendLandingRecord(t, path, "replacement")
			if got := p.look(); got != landingRefused {
				t.Fatalf("a replacement source landed the answer: %v", got)
			}
		})
	}
}

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
		{"an unidentified call ran", true, []hooks.Event{ev(status.Working, "PostToolUse")}, false, false},
		{"another dialog came up", true, []hooks.Event{ev(status.Waiting, "Notification"), ev(status.Working, "PostToolUse")}, false, true},
		{"the turn stopped without the call", true, []hooks.Event{ev(status.Finished, "Stop")}, false, true},
		{"background call before a new question", true, []hooks.Event{ev(status.Working, "PostToolUse"), ev(status.Waiting, "PreToolUse")}, false, true},
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
		{"another call answered", true, convo.Delta{Results: []convo.Result{{ToolUseID: "background"}}}, false, false},
		{"another call declined", true, convo.Delta{Results: []convo.Result{{ToolUseID: "background", Rejected: true}}}, false, false},
		{"a dialog declined", true, convo.Delta{Results: []convo.Result{{ToolUseID: "t1", Rejected: true}}}, false, true},
		{"a turn interrupted", false, convo.Delta{Prompts: []string{"[Request interrupted by user]", "go on"}}, false, true},
		{"nothing yet", true, convo.Delta{}, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &landingProbe{dialog: c.dialog, toolID: "t1"}
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

// Each look is counted and the source that decided is named, so a recorded
// landing says what the operator waited on.
func TestALookNamesTheSourceThatDecided(t *testing.T) {
	working := false
	p := &landingProbe{dialog: true, paneWorking: func() bool { return working }}
	p.look()
	if p.decided != "" {
		t.Fatalf("a pending look named %q", p.decided)
	}
	working = true
	p.look()
	if p.decided != "pane" || p.looks != 2 || p.sources() != "pane" {
		t.Fatalf("decided %q after %d looks over %q, want pane after 2 over pane", p.decided, p.looks, p.sources())
	}

	m := buildModel(t)
	logHookEvent(t, m, "declined", "waiting Notification")
	refusing := &landingProbe{dialog: true, hooks: m.hooks, hookID: "declined", paneWorking: func() bool { return true }}
	refusing.look()
	if refusing.decided != "hooks-boundary" || refusing.sources() != "hooks,pane" {
		t.Fatalf("decided %q over %q, want hooks-boundary over hooks,pane", refusing.decided, refusing.sources())
	}
}

// A look for an older key must not decide the answer that replaced it.
func TestAStaleLookDecidesNothing(t *testing.T) {
	m := drainOnDialog(t, true)
	m = pressDialogKey(m)
	askID := focusedID(t, m)
	stale := onlyLanding(t, m).gen
	m = pressDialogKey(m)
	m.applyLandingCheck(landingCheckMsg{id: askID, gen: stale, verdict: landingSeen})
	if pending := onlyLanding(t, m); pending.gen == stale {
		t.Fatal("a look for the replaced key decided the pending answer")
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("a stale look handed the session over to %q", got)
	}
}

func TestPasteKeepsALandedAnswerOnItsSession(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m = pressDialogKey(m)
	m.handleFocusPaste(tea.PasteMsg{Content: "one more thing"})
	recordDialogResult(t, m, askID, "answered")
	lookForLanding(t, m)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("paste handed the session over to %q", got)
	}
	if m.latestSubmission.sessionID != askID {
		t.Fatal("paste prevented recording the landed answer")
	}
}

// A snippet from the hotkey menu that is never seen landing records nothing
// and moves the drain nowhere.
func TestAHotkeyMenuSendThatNeverLandsStaysPut(t *testing.T) {
	m := enterDrain(t, drainFleet(t))
	bindMenuSnippet(t, m, "Please continue")
	sess, _ := m.selected()
	m.openQuickMode()
	if _, _ = m.handleQuickKey(letter('c')); !strings.HasPrefix(m.errBar.text, "sent ") {
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
	recordDialogResult(t, m, askID, "answered")
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

func TestLandingTimingIncludesBaselineWithoutShorteningWindow(t *testing.T) {
	m := &Model{}
	started := time.Now().Add(-5 * time.Second)
	beforeArm := time.Now()
	m.armLanding(store.Session{ID: "row"}, started, extension.OperatorPane, "", false, true, "")
	pending := m.landings["row"]
	if pending.deadline.Before(beforeArm.Add(landingWindow)) {
		t.Fatal("baseline time shortened the acceptance window")
	}
	spans := recordSpans(t, func() { recordLanding(pending, "timeout") })
	span := oneSpan(t, spans, "ui.landing")
	if span.began(t) != started.UnixNano() || span.number(t, "took_ms") < 5000 {
		t.Fatal("landing timing omitted the time before arming")
	}
}

func TestPollRecordsLandingWhileLookIsInFlight(t *testing.T) {
	m := drainOnDialog(t, true)
	id := focusedID(t, m)
	m = pressDialogKey(m)
	pending := m.landings[id]
	path := filepath.Join(t.TempDir(), "landing.log")
	logger, err := logging.Open(logging.Options{Path: path, Level: logging.LevelInfo})
	if err != nil {
		t.Fatal(err)
	}
	previous := logging.SetDefault(logger)
	t.Cleanup(func() { logging.SetDefault(previous); logger.Close() })
	entered, release := make(chan struct{}), make(chan struct{})
	pending.probe = &landingProbe{paneWorking: func() bool {
		close(entered)
		<-release
		return true
	}}
	pending.observed = *pending.probe
	result := make(chan tea.Msg, 1)
	go func() { result <- landingLook(id, pending)() }()
	<-entered
	spans := recordSpans(t, func() {
		pollReporting(t, m, "ask", status.Working)
		close(release)
		m.applyLandingCheck((<-result).(landingCheckMsg))
	})
	landings := spansNamed(spans, "ui.landing")
	if len(landings) != 1 {
		t.Fatalf("poll and stale check recorded %d landings, want one", len(landings))
	}
	if landings[0].str(t, "outcome") != "seen" || landings[0].str(t, "by") != "poll" {
		t.Fatal("poll landing lost its outcome or evidence source")
	}
	if focusedName(t, m) != "next" || len(m.landings) != 0 {
		t.Fatal("poll did not finish the pending handover")
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(blob), "triage answer") != 1 {
		t.Fatalf("poll landing log = %s", blob)
	}
}

func TestReplacingLandingRecordsDisplacedAnswerOnce(t *testing.T) {
	m := &Model{}
	sess := store.Session{ID: "row"}
	path := filepath.Join(t.TempDir(), "landing.log")
	logger, err := logging.Open(logging.Options{Path: path, Level: logging.LevelInfo})
	if err != nil {
		t.Fatal(err)
	}
	previous := logging.SetDefault(logger)
	t.Cleanup(func() { logging.SetDefault(previous); logger.Close() })
	first := m.armLanding(sess, time.Now(), extension.OperatorPane, "", false, true, "")
	old := m.landings[sess.ID]
	spans := recordSpans(t, func() {
		m.armLanding(sess, time.Now(), extension.OperatorPane, "", false, true, "")
		next := m.landings[sess.ID]
		if next == old {
			t.Fatal("new answer did not replace the pending landing")
		}
		m.applyLandingCheck(first().(landingCheckMsg))
		if m.landings[sess.ID] != next {
			t.Fatal("stale check changed the replacement landing")
		}
		m.applyLandingCheck(landingCheckMsg{id: sess.ID, gen: next.gen, verdict: landingRefused})
	})
	landings := spansNamed(spans, "ui.landing")
	if len(landings) != 2 || landings[0].str(t, "outcome") != "replaced" || landings[1].str(t, "outcome") != "refused" {
		t.Fatalf("replacement and final landing spans = %v", landings)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(blob), "triage answer") != 2 || !strings.Contains(string(blob), "outcome=replaced") {
		t.Fatalf("replacement landing log = %s", blob)
	}
}
