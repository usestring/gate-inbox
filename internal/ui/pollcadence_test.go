package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestBackgroundPaneCadences(t *testing.T) {
	now := time.Now()
	p := &poller{interval: 2 * time.Second}
	for _, tc := range []struct {
		state    string
		interval time.Duration
	}{
		{status.Working, 4 * time.Second},
		{status.Starting, 2 * time.Second},
		{status.Idle, 15 * time.Second},
		{status.Waiting, 15 * time.Second},
		{status.Finished, 15 * time.Second},
		{status.Errored, 15 * time.Second},
	} {
		t.Run(tc.state, func(t *testing.T) {
			sess := store.Session{ID: "background", Tool: "codex", Status: tc.state}
			sample := paneSample{binding: p.paneBinding(sess), capture: tmux.Capture{At: now}}
			if p.paneDue(sess, sample, "focused", now.Add(tc.interval-time.Nanosecond), hookStamp{}, false, false) {
				t.Fatal("background pane was sampled before its interval")
			}
			if !p.paneDue(sess, sample, "focused", now.Add(tc.interval), hookStamp{}, false, false) {
				t.Fatal("background pane was not sampled when due")
			}
			if !p.paneDue(sess, sample, sess.ID, now, hookStamp{}, false, false) {
				t.Fatal("focused pane waited for a background interval")
			}
		})
	}
	for _, sess := range []store.Session{
		{Status: status.Waiting, ParentID: "parent"},
		{Status: status.Finished, SpawnedBy: "parent"},
	} {
		if got := p.paneInterval(sess); got != p.interval {
			t.Fatalf("child automation cadence = %v, want %v", got, p.interval)
		}
	}
	p.interval = 30 * time.Second
	if got := p.paneInterval(store.Session{Status: status.Working}); got != p.interval {
		t.Fatal("background cadence shortened the configured poll interval")
	}
}

func TestBackgroundPaneWakesOnChanges(t *testing.T) {
	now := time.Now()
	for _, reason := range []string{"manual", "queued", "pending input", "operator input", "settled send", "hook", "binding", "pin", "capture failure", "first look"} {
		t.Run(reason, func(t *testing.T) {
			p := &poller{interval: 2 * time.Second, operatorInputAt: map[string]time.Time{}, settledAt: map[string]time.Time{}}
			sess := store.Session{ID: "background", Tool: "codex", Status: status.Idle}
			sample := paneSample{binding: p.paneBinding(sess), capture: tmux.Capture{At: now.Add(-time.Second)}}
			var hook hookStamp
			queued, force := false, false
			switch reason {
			case "manual":
				force = true
			case "queued":
				queued = true
			case "pending input":
				sess.PendingInputs = []string{"continue"}
			case "operator input":
				p.noteOperatorInput(sess.ID)
			case "settled send":
				p.settledAt[sess.ID] = now
			case "hook":
				hook = hookStamp{size: 1, modified: now}
			case "binding":
				sess.AgentSessionID = "new-conversation"
			case "pin":
				p.pins = fixedPins{sess.ID: status.Waiting}
			case "capture failure":
				sample.capture.Err = errors.New("unreadable server")
			case "first look":
				sample.capture.At = time.Time{}
			}
			if !p.paneDue(sess, sample, "focused", now, hook, queued, force) {
				t.Fatal("changed background pane was held behind its cadence")
			}
		})
	}
	p := &poller{interval: 2 * time.Second, operatorInputAt: map[string]time.Time{"elsewhere": now}}
	sess := store.Session{ID: "background", Status: status.Idle}
	sample := paneSample{binding: p.paneBinding(sess), capture: tmux.Capture{At: now.Add(-time.Second)}}
	if p.paneDue(sess, sample, "focused", now, hookStamp{}, false, false) {
		t.Fatal("input in another pane woke a stable session")
	}
}

func scheduledPass(t *testing.T, p *poller) (refreshMsg, passStat) {
	t.Helper()
	var stats passStat
	msg := p.refreshPass(&stats, false)
	result, ok := msg.(refreshMsg)
	if !ok {
		t.Fatalf("background pass: %#v", msg)
	}
	return result, stats
}

func cadenceSession(t *testing.T, m *Model, tool string) store.Session {
	t.Helper()
	id, err := m.spawnSessionAs(tool, "", "worker", t.TempDir(), "", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := m.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range sessionPendingInputs(t, m, sess.ID) {
		if claimed, err := m.store.ClaimPendingInput(sess.ID, input); err != nil || !claimed {
			t.Fatalf("claim fixture launch input: %v", err)
		}
		if consumed, err := m.store.ConsumeClaimedPendingInput(sess.ID, input); err != nil || !consumed {
			t.Fatalf("consume fixture launch input: %v", err)
		}
	}
	if err := m.tmux.SendKeys(sess.ID, "\n❯ "); err != nil {
		t.Fatal(err)
	}
	settledPane(t, m, sess.ID, "❯")
	return sess
}

func TestScheduledPollSkipsStablePanesButPreservesTheirRows(t *testing.T) {
	m := buildModel(t)
	stable := cadenceSession(t, m, "ready-tool")
	working := cadenceSession(t, m, "claude-hooked")
	writeHookStatus(t, m, working.ID, status.Working)
	settledPane(t, m, stable.ID, "❯")
	if failed, ok := m.poller.refreshOnce().(errMsg); ok {
		t.Fatal(failed.err)
	}
	m.poller.setInput(false, working.ID)
	before := m.poller.paneSamples[stable.ID]
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	result, stats := scheduledPass(t, m.poller)
	if stats.live != 2 || stats.captured != 1 {
		t.Fatalf("live = %d, captured = %d; want only the selected pane captured", stats.live, stats.captured)
	}
	if result.snapOK {
		t.Fatal("an immediate focus refresh resampled the machine gauges")
	}
	if after := m.poller.paneSamples[stable.ID]; !after.capture.At.Equal(before.capture.At) || !after.procAt.Equal(before.procAt) {
		t.Fatal("stable pane or process tree was sampled again")
	}
	if !strings.Contains(result.searchText[stable.ID], "❯") || len(result.sessions) != 2 {
		t.Fatal("skipped pane disappeared from search or the board")
	}
	for _, row := range result.sessions {
		if row.ID == stable.ID && row.Status != status.Idle {
			t.Fatalf("skipped idle row became %s", row.Status)
		}
	}
	if len(observer.moves) != 0 || result.agents.count != 2 {
		t.Fatal("skipping a pane invented a transition or dropped its process stats")
	}
	m.poller.setInput(false, "")
	_, stats = scheduledPass(t, m.poller)
	if stats.captured != 0 {
		t.Fatalf("unchanged background sessions captured = %d, want 0", stats.captured)
	}
	sample := m.poller.paneSamples[working.ID]
	sample.capture.At = time.Now().Add(-backgroundWorkingInterval)
	m.poller.paneSamples[working.ID] = sample
	_, stats = scheduledPass(t, m.poller)
	if stats.captured != 1 || !m.poller.paneSamples[stable.ID].capture.At.Equal(before.capture.At) {
		t.Fatal("working interval did not refresh only the working pane")
	}
	if _, ok := m.poller.refreshOnce().(refreshMsg); !ok || !m.poller.paneSamples[stable.ID].capture.At.After(before.capture.At) {
		t.Fatal("explicit refresh did not bypass the stable cadence")
	}
}

func TestScheduledPollWakesForHooksAndQueuedMessages(t *testing.T) {
	m := buildModel(t)
	sess := cadenceSession(t, m, "claude-hooked")
	writeHookStatus(t, m, sess.ID, status.Idle)
	settledPane(t, m, sess.ID, "")
	m.poller.setInput(false, "")
	scheduledPass(t, m.poller)
	_, stats := scheduledPass(t, m.poller)
	if stats.captured != 0 {
		t.Fatal("unchanged idle row was not throttled")
	}
	writeHookStatus(t, m, sess.ID, status.Waiting)
	result, stats := scheduledPass(t, m.poller)
	if stats.captured != 1 || result.sessions[0].Status != status.Waiting {
		t.Fatal("new hook did not immediately make the waiting row available")
	}
	writeHookStatus(t, m, sess.ID, status.Idle)
	scheduledPass(t, m.poller)
	id := queueMessage(t, m, sess.ID, "perform the queued task")
	_, stats = scheduledPass(t, m.poller)
	if stats.captured != 1 || !m.poller.awaitSends(3*time.Second) {
		t.Fatal("queued message waited behind the stable cadence")
	}
	message, err := m.store.Message(id, "sender01")
	if err != nil || message.DeliveredAt.IsZero() {
		t.Fatalf("queued message not delivered: %+v, %v", message, err)
	}
}

func TestScheduledPollStillDetectsAnUnfocusedPaneClosing(t *testing.T) {
	m := buildModel(t)
	sess := cadenceSession(t, m, "ready-tool")
	m.poller.setInput(false, "")
	settledPane(t, m, sess.ID, "❯")
	scheduledPass(t, m.poller)
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatal(err)
	}
	result, _ := scheduledPass(t, m.poller)
	if len(result.sessions) != 1 || result.sessions[0].Status != status.Dead || len(m.poller.paneSamples) != 0 {
		t.Fatal("stable cadence hid a closed pane or retained its cache")
	}
}

func TestScheduledPollPreservesQuestionsWithoutRereadingASkippedPane(t *testing.T) {
	m := buildModel(t)
	pane := readDialogFixture(t, "claude-2.1.283-tabs-w50-second.ansi")
	tool := m.cfg.Tools["claude"]
	tool.Command = "sh -c " + tmux.ShellQuote("printf %s "+tmux.ShellQuote(pane)+"; exec cat"+soleWriter)
	m.cfg.Tools["claude"] = tool
	sess := cadenceSession(t, m, "claude")
	if err := m.store.SetAgentSessionID(sess.ID, "conv-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.UpdateStatus(sess.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "claude-ask-four-questions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeClaudeTranscript(t, "conv-1", fixture)
	m.poller.setInput(false, "")
	first, _ := scheduledPass(t, m.poller)
	if len(first.askQuestions[sess.ID]) != 4 {
		t.Fatal("first capture did not read the pane's four questions")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	cached, stats := scheduledPass(t, m.poller)
	if stats.captured != 0 || len(cached.askQuestions[sess.ID]) != 4 || m.poller.askReads[sess.ID] == nil {
		t.Fatal("skipping the pane lost its question card or reread its transcript")
	}
	m.poller.setInput(false, sess.ID)
	fresh, stats := scheduledPass(t, m.poller)
	if stats.captured != 1 || len(fresh.askQuestions[sess.ID]) != 0 {
		t.Fatal("focusing the pane did not refresh its question source")
	}
}

func TestSampledProcUsesEachPanesOwnElapsedTime(t *testing.T) {
	now := time.Now()
	previous := paneSample{pid: 42, proc: sysstat.ProcStat{OK: true, CPUSeconds: 10}, procAt: now.Add(-15 * time.Second)}
	stat := sysstat.ProcStat{OK: true, CPUSeconds: 40, RSS: 100, PCPU: 80}
	got := sampledProc(previous, 42, stat, now, 4, 1000)
	if got.CPUPercent != 50 || got.RamPercent != 10 {
		t.Fatalf("sample = %+v, want 50%% CPU over 15s and 10%% RAM", got)
	}
	if got := sampledProc(previous, 43, stat, now, 4, 1000); got.CPUPercent != 20 {
		t.Fatal("a replacement pane inherited the old process's CPU baseline")
	}
	stat.CPUSeconds = 5
	if got := sampledProc(previous, 42, stat, now, 4, 1000); got.CPUPercent != 0 {
		t.Fatal("a reset CPU counter produced negative usage")
	}
}

func TestSelectingAPaneWakesTheBackgroundPoller(t *testing.T) {
	p := &poller{poke: make(chan struct{}, 1)}
	p.setInput(false, "focused")
	select {
	case <-p.poke:
	default:
		t.Fatal("selection change did not request an immediate pass")
	}
	p.setInput(false, "focused")
	select {
	case <-p.poke:
		t.Fatal("unchanged selection requested another pass")
	default:
	}
}

func TestBackgroundPaneKeepsPollingAfterOperatorSubmission(t *testing.T) {
	at := time.Now()
	p := &poller{interval: 2 * time.Second}
	sess := store.Session{ID: "background", Tool: "codex", Status: status.Finished, OperatorAt: at}
	sample := paneSample{binding: p.paneBinding(sess), capture: tmux.Capture{At: at.Add(100 * time.Millisecond)}}
	if !p.paneDue(sess, sample, "focused", at.Add(2*time.Second), hookStamp{}, false, false) {
		t.Fatal("the first post-submit capture hid a delayed redraw behind the stable cadence")
	}
	sample.capture.At = at.Add(2 * time.Second)
	if !p.paneDue(sess, sample, "focused", at.Add(4*time.Second), hookStamp{}, false, false) {
		t.Fatal("no final capture after the submission grace")
	}
	sample.capture.At = at.Add(4 * time.Second)
	if p.paneDue(sess, sample, "focused", at.Add(6*time.Second), hookStamp{}, false, false) {
		t.Fatal("post-submit polling did not return to the stable cadence")
	}
}
