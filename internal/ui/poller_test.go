// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func newTestPollerWithSession(t *testing.T) (*poller, store.Session) {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	sess := store.Session{ID: "sess-1", Name: "one", Tool: "codex", Cwd: t.TempDir(), Group: "g", Status: "idle"}
	if err := st.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}

	hookManager := hooks.NewManager(t.TempDir())
	p := &poller{store: st, hooks: hookManager}
	return p, got
}

// A detached session must boot at the preview panel's width×height so its
// pane preview fills 1:1, and follow later terminal resizes, rather than
// staying at tmux's 80×24 default until attach.
func TestSessionSizesToPreviewPane(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "sized", t.TempDir(), "")
	id := m.sessionRows()[0].ID
	// Create sizes from pre-selection geometry; re-pin to the live preview box.
	m.resizeNow(t)

	wantW, wantH := m.previewPaneWidth(), m.previewPaneHeight()
	if w, h := windowSize(t, id); w != wantW || h != wantH {
		t.Fatalf("new session window = %dx%d, want %dx%d", w, h, wantW, wantH)
	}

	m.resizeWindow(t, 150, 45)
	wantW, wantH = m.previewPaneWidth(), m.previewPaneHeight()
	if w, h := windowSize(t, id); w != wantW || h != wantH {
		t.Fatalf("after resize, window = %dx%d, want %dx%d", w, h, wantW, wantH)
	}
}

func TestPendingRenameForADeletedSessionDoesNotFailThePoll(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "doomed", t.TempDir(), "")
	sess := m.sessionRows()[0]

	// The manager deleted the row while this poll pass still held it.
	if err := m.store.Delete(sess.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	nameFile := m.hooks.NameFile(sess.ID)
	if err := os.MkdirAll(filepath.Dir(nameFile), 0o755); err != nil {
		t.Fatalf("hooks dir: %v", err)
	}
	if err := os.WriteFile(nameFile, []byte("renamed"), 0o644); err != nil {
		t.Fatalf("write name file: %v", err)
	}

	if err := m.poller.applyPendingRename(&sess); err != nil {
		t.Fatalf("rename of a deleted session should not fail the pass: %v", err)
	}
	if _, found := m.hooks.ReadName(sess.ID); found {
		t.Fatal("the name file should be consumed instead of retried every poll")
	}
}

func writeName(t *testing.T, m *Model, id, name string) {
	t.Helper()
	path := m.hooks.NameFile(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("hooks dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
		t.Fatalf("write name file: %v", err)
	}
}

func writeHookStatus(t *testing.T, m *Model, id, state string) {
	t.Helper()
	path := m.hooks.StatusFile(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(state), 0o644); err != nil {
		t.Fatalf("write hook status: %v", err)
	}
}

func deriveStatus(t *testing.T, m *Model, sess store.Session, pane string, agentAlive bool) string {
	t.Helper()
	got, err := m.poller.derivePaneStatus(sess, pane, agentAlive, map[string]uint64{})
	if err != nil {
		t.Fatalf("derivePaneStatus: %v", err)
	}
	return got
}

func TestHookStatusDerivesFinishedAndIdleWhenAcked(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked01", Tool: "claude-hooked"}
	pane := "some output\n❯ \n"
	writeHookStatus(t, m, sess.ID, status.Finished)

	if got := deriveStatus(t, m, sess, pane, true); got != status.Finished {
		t.Fatalf("hook finished should derive finished, got %q", got)
	}

	sess.Acked = true
	if got := deriveStatus(t, m, sess, pane, true); got != status.Idle {
		t.Fatalf("acked hook finished should derive idle, got %q", got)
	}
}

func TestHookWorkingWinsOverUnmatchedPane(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked02", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Working)

	pane := "plain streaming text no rule matches\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("hook working should win, got %q", got)
	}
}

func TestHookFinishedUpgradesToWaitingOnQuestionTurn(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked03", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Finished)

	pane := "Do you want me to proceed?\n\n✻ Baked for 5s\n\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Waiting {
		t.Fatalf("question turn should upgrade hook finished to waiting, got %q", got)
	}
}

func TestHookFinishedUpgradesToErroredOnErrorLine(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked04", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Finished)

	pane := "error: something broke\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Errored {
		t.Fatalf("error line should upgrade hook finished to errored, got %q", got)
	}
}

func TestHookWorkingUpgradesToWaitingOnPaneMatch(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked05", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Working)

	pane := "Enter to confirm\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Waiting {
		t.Fatalf("waiting pane verdict should upgrade hook working, got %q", got)
	}
}

func TestHookWorkingReconcilesToFinishedOnEndedTurn(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked07", Tool: "claude-hooked", Status: status.Working}
	writeHookStatus(t, m, sess.ID, status.Working)

	pane := "here is the result\n\n✻ Baked for 5s\n\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Finished {
		t.Fatalf("stale working hook over an ended turn should reconcile to finished, got %q", got)
	}
}

// Claude fires Stop when the main agent stops responding, so a turn that
// leaves background agents running reports finished while they work. The
// pane still shows the wait, and that verdict has to win.
func TestHookErroredReconcilesToPaneVerdict(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked-err", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Errored)

	if got := deriveStatus(t, m, sess, "Enter to confirm\n❯ \n", true); got != status.Waiting {
		t.Fatalf("waiting pane should override hook errored, got %q", got)
	}
	if got := deriveStatus(t, m, sess,
		"⏺ Security agent done. 2 left.\n✻ Waiting for 2 background agents to finish\n❯ \n", true); got != status.Working {
		t.Fatalf("working pane should override hook errored, got %q", got)
	}
	if got := deriveStatus(t, m, sess, "here is the result\n\n✻ Baked for 5s\n\n❯ \n", true); got != status.Finished {
		t.Fatalf("finished pane should override hook errored, got %q", got)
	}

	sess.Acked = true
	if got := deriveStatus(t, m, sess, "here is the result\n\n✻ Baked for 5s\n\n❯ \n", true); got != status.Idle {
		t.Fatalf("acked finished pane should idle over hook errored, got %q", got)
	}
}

func TestHookFinishedUpgradesToErroredOnUsageLimit(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked-limit", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Finished)

	pane := "  ⎿  You've hit your weekly limit · resets 1am (Asia/Jerusalem)\n\n" +
		"✻ Churned for 2h 0m 54s\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Errored {
		t.Fatalf("a usage limit should read as errored, got %q", got)
	}
}

func TestHookFinishedUpgradesToWorkingWhileBackgroundAgentsRun(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked08", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Finished)

	pane := "⏺ Security agent done. 2 left.\n✻ Waiting for 2 background agents to finish\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("background agents still running should upgrade hook finished to working, got %q", got)
	}

	sess.Acked = true
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("an acked session with background agents running should still read working, got %q", got)
	}
}

// A background shell outlives its turn the same way, and Stop fires the
// moment the main agent stops responding, so the hook reports finished
// while the shell runs and the notification for it would fire early.
func TestHookFinishedUpgradesToWorkingWhileBackgroundShellsRun(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked10", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Finished)

	pane := "⏺ ok\n✻ Worked for 3s · 1 shell still running\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("a running background shell should upgrade hook finished to working, got %q", got)
	}
}

// The wait line disappears once the agents drain, and the completed turn
// below it must settle back to the hook's own verdict.
func TestHookFinishedStaysFinishedOnceBackgroundAgentsDrain(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked09", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Finished)

	pane := "⏺ all agents reported\n✻ Worked for 5s\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Finished {
		t.Fatalf("drained background agents should read finished, got %q", got)
	}
}

func TestStaleHookFileFallsBackToPaneRules(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "hooked06", Tool: "claude-hooked"}
	writeHookStatus(t, m, sess.ID, status.Working)

	pane := "shell prompt after a crash\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, false); got != status.Idle {
		t.Fatalf("dead agent should fall back to pane rules, got %q", got)
	}
	if _, ok := m.hooks.Read(sess.ID); ok {
		t.Fatal("stale hook status file should be removed")
	}
}

func seedRegionHash(t *testing.T, m *Model, sess store.Session, pane string) {
	t.Helper()
	region, ok := m.poller.engine.ActivityRegion(sess.Tool, ansi.Strip(pane))
	if !ok {
		t.Fatal("pane should have an activity region")
	}
	m.poller.paneHashes = map[string]uint64{sess.ID: activityFingerprint(region)}
}

func disableQuietEndGrace(t *testing.T) {
	t.Helper()
	prev := quietEndGrace
	quietEndGrace = 0
	t.Cleanup(func() { quietEndGrace = prev })
}

func TestQuietPaneAfterWorkingDerivesFinished(t *testing.T) {
	disableQuietEndGrace(t)
	m := buildModel(t)
	sess := store.Session{ID: "quiet01", Tool: "claude-hooked", Status: status.Working}
	pane := "final answer with no turn marker\n❯ \n"
	seedRegionHash(t, m, sess, pane)
	if got := deriveStatus(t, m, sess, pane, true); got != status.Finished {
		t.Fatalf("quiet pane after working should derive finished, got %q", got)
	}
}

func TestQuietCodexPaneQuotingInterruptHintDerivesFinished(t *testing.T) {
	disableQuietEndGrace(t)
	m := buildModel(t)
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	m.poller.engine, err = status.NewEngine(cfg)
	if err != nil {
		t.Fatalf("status engine: %v", err)
	}
	sess := store.Session{ID: "quiet-codex", Tool: "codex", Status: status.Working}
	pane := "Output:\n\ntool: mytool\nresult: working\npattern: esc to interrupt\ndefault: idle\n\n› Summarize recent commits\n"
	seedRegionHash(t, m, sess, pane)
	if got := deriveStatus(t, m, sess, pane, true); got != status.Finished {
		t.Fatalf("quiet Codex pane quoting its interrupt hint should derive finished, got %q", got)
	}
}

func TestCodexComposerAnimationDoesNotKeepTurnWorking(t *testing.T) {
	disableQuietEndGrace(t)
	for _, tc := range []struct{ content, want string }{
		{"• Done.\n", status.Finished},
		{"• Continue?\n", status.Waiting},
		{"• Working (12s • esc to interrupt)\n", status.Working},
	} {
		m := buildModel(t)
		cfg, err := config.Default()
		if err != nil {
			t.Fatal(err)
		}
		m.poller.engine, err = status.NewEngine(cfg)
		if err != nil {
			t.Fatal(err)
		}
		sess := store.Session{ID: "animated-codex", Tool: "codex", Status: status.Working}
		first := tc.content + "\n⠁ ⠈ ⢀ ⡀\n› Ask Codex to do anything\n"
		second := tc.content + "\n⠄ ⠠ ⠂\n› Ask Codex to do anything\n"
		seedRegionHash(t, m, sess, first)
		if got := deriveStatus(t, m, sess, second, true); got != tc.want {
			t.Fatalf("animation over %q = %q; want %q", tc.content, got, tc.want)
		}
		streaming := strings.Replace(second, tc.content, tc.content+"• New output\n", 1)
		if got := deriveStatus(t, m, sess, streaming, true); got != status.Working {
			t.Fatalf("new output = %q; want working", got)
		}
	}
}

func TestQuietPaneHoldsWorkingUntilGrace(t *testing.T) {
	prev := quietEndGrace
	quietEndGrace = time.Hour
	t.Cleanup(func() { quietEndGrace = prev })
	m := buildModel(t)
	sess := store.Session{ID: "quiet-hold", Tool: "claude-hooked", Status: status.Working}
	pane := "final answer with no turn marker\n❯ \n"
	seedRegionHash(t, m, sess, pane)
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("first quiet poll within grace should stay working, got %q", got)
	}
}

func TestQuietPaneEndingOnQuestionDerivesWaiting(t *testing.T) {
	disableQuietEndGrace(t)
	m := buildModel(t)
	sess := store.Session{ID: "quiet02", Tool: "claude-hooked", Status: status.Working}
	pane := "Which of the two options do you prefer?\n❯ \n"
	seedRegionHash(t, m, sess, pane)
	if got := deriveStatus(t, m, sess, pane, true); got != status.Waiting {
		t.Fatalf("quiet pane ending on a question should derive waiting, got %q", got)
	}
}

func TestQuietPaneAfterIdleStaysIdle(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "quiet03", Tool: "claude-hooked", Status: status.Idle}
	pane := "old transcript text\n❯ \n"
	seedRegionHash(t, m, sess, pane)
	if got := deriveStatus(t, m, sess, pane, true); got != status.Idle {
		t.Fatalf("quiet pane after idle should stay idle, got %q", got)
	}
}

func TestQuietFinishedPersistsAndAckMapsToIdle(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "quiet04", Tool: "claude-hooked", Status: status.Finished}
	pane := "final answer with no turn marker\n❯ \n"
	seedRegionHash(t, m, sess, pane)
	if got := deriveStatus(t, m, sess, pane, true); got != status.Finished {
		t.Fatalf("inferred finished should persist while the pane stays quiet, got %q", got)
	}
	sess.Acked = true
	if got := deriveStatus(t, m, sess, pane, true); got != status.Idle {
		t.Fatalf("acked inferred finished should derive idle, got %q", got)
	}
}

func TestChangedRegionStillDerivesWorking(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "quiet05", Tool: "claude-hooked", Status: status.Working}
	seedRegionHash(t, m, sess, "earlier streaming text\n❯ \n")
	if got := deriveStatus(t, m, sess, "earlier streaming text plus more\n❯ \n", true); got != status.Working {
		t.Fatalf("changed region should derive working, got %q", got)
	}
}

// A post-resize rebaseline (no prior hash) must keep finished instead of
// inventing working from reflowed content or collapsing to idle.
func TestRebaselineKeepsFinishedWithoutFlashingWorking(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "reflow01", Tool: "claude-hooked", Status: status.Finished}
	before := "final answer line that wraps differently after resize\n❯ \n"
	after := "final answer line that wraps\ndifferently after resize\n❯ \n"
	seedRegionHash(t, m, sess, before)
	// Without clearing, a reflow looks like streaming work.
	if got := deriveStatus(t, m, sess, after, true); got != status.Working {
		t.Fatalf("reflow with a prior hash should look like working (precondition), got %q", got)
	}
	seedRegionHash(t, m, sess, before)
	m.poller.reflowSessions([]string{sess.ID}, func() {})
	if got := deriveStatus(t, m, sess, after, true); got != status.Finished {
		t.Fatalf("rebaseline after resize must keep finished, got %q", got)
	}
}

// The wheel and a click are forwarded into the pane and the agent redraws
// itself around them. That redraw is the operator working, not the agent,
// so it must not turn an idle row into a working one (and, once it goes
// quiet again, into a finished alert nobody earned).
func TestForwardedMouseInputDoesNotDeriveWorking(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "scroll01", Tool: "claude-hooked", Status: status.Idle}
	onScreen := "line one\nline two\n❯ \n"
	scrolledBack := "older line\nline one\n❯ \n"
	seedRegionHash(t, m, sess, onScreen)
	// Without the guard the repaint is indistinguishable from streaming.
	if got := deriveStatus(t, m, sess, scrolledBack, true); got != status.Working {
		t.Fatalf("a repaint with a prior hash should look like working (precondition), got %q", got)
	}
	seedRegionHash(t, m, sess, onScreen)
	m.poller.noteOperatorInput(sess.ID)
	if got := deriveStatus(t, m, sess, scrolledBack, true); got != status.Idle {
		t.Fatalf("scrolling an idle session must leave it idle, got %q", got)
	}
}

// The guard rebaselines rather than freezing: a session mid-turn keeps the
// status it had while the operator scrolls, exactly as a resize does.
func TestForwardedMouseInputHoldsAnInFlightTurn(t *testing.T) {
	m := buildModel(t)
	for _, st := range []string{status.Working, status.Waiting, status.Finished} {
		sess := store.Session{ID: "scroll-" + st, Tool: "claude-hooked", Status: st}
		seedRegionHash(t, m, sess, "line one\n❯ \n")
		m.poller.noteOperatorInput(sess.ID)
		if got := deriveStatus(t, m, sess, "older line\nline one\n❯ \n", true); got != st {
			t.Fatalf("scrolling a %q session: got %q", st, got)
		}
	}
}

// Suppressing forever would hide the agent from an operator who is reading
// its output, so the guard lasts only as long as the repaint can still be
// arriving: a poll interval plus the settle grace.
func TestMouseEchoExpiresSoStreamingIsSeenAgain(t *testing.T) {
	m := buildModel(t)
	m.poller.interval = 0
	prev := operatorEchoGrace
	operatorEchoGrace = time.Millisecond
	t.Cleanup(func() { operatorEchoGrace = prev })

	sess := store.Session{ID: "scroll02", Tool: "claude-hooked", Status: status.Idle}
	seedRegionHash(t, m, sess, "line one\n❯ \n")
	m.poller.noteOperatorInput(sess.ID)
	time.Sleep(5 * time.Millisecond)
	if got := deriveStatus(t, m, sess, "line one\nstreaming text\n❯ \n", true); got != status.Working {
		t.Fatalf("an expired echo must not keep hiding real streaming, got %q", got)
	}
	if _, held := m.poller.operatorInputAt[sess.ID]; held {
		t.Fatal("an expired stamp should be dropped rather than kept per session forever")
	}
}

// The mouse-echo guard answers "did my own forwarded input cause this
// repaint", which is a claim about change, so it stays scoped to the
// region-hash inference and never touches the rules. Whether the screen has
// stopped describing the present is a different question, answered by the
// tool's own displaced-viewport affordance -- absent from this pane, which
// is therefore at its live bottom with a spinner really on it. Reading that
// as anything but working would blind the board to a turn that started
// while the operator happened to be clicking.
func TestMouseEchoDoesNotBlindTheRules(t *testing.T) {
	m := buildModel(t)
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	m.poller.engine, err = status.NewEngine(cfg)
	if err != nil {
		t.Fatalf("status engine: %v", err)
	}
	sess := store.Session{ID: "scroll03", Tool: "claude", Status: status.Idle}
	seedRegionHash(t, m, sess, "line one\n❯ \n")
	m.poller.noteOperatorInput(sess.ID)
	pane := "✳ Drizzling… (6s · thinking)\n  esc to interrupt\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Working {
		t.Fatalf("a spinner during a scroll must still read working, got %q", got)
	}
}

func TestRebaselineKeepsWaitingAndWorking(t *testing.T) {
	m := buildModel(t)
	pane := "Which option do you prefer?\n❯ \n"
	for _, st := range []string{status.Waiting, status.Working} {
		sess := store.Session{ID: "reflow-" + st, Tool: "claude-hooked", Status: st}
		if got := deriveStatus(t, m, sess, pane, true); got != st {
			t.Fatalf("unseen baseline with status %q: got %q", st, got)
		}
	}
}

func TestRebaselineIdleStaysIdle(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "reflow-idle", Tool: "claude-hooked", Status: status.Idle}
	pane := "old transcript text\n❯ \n"
	if got := deriveStatus(t, m, sess, pane, true); got != status.Idle {
		t.Fatalf("unseen baseline idle should stay idle, got %q", got)
	}
}

func TestLiveQuietTurnResolvesFinished(t *testing.T) {
	m := buildModel(t)
	m.openForm()
	m.form.name.SetValue("quiet-live")
	m.form.dir.SetValue(t.TempDir())
	for i, name := range sortedToolNames(m.cfg) {
		if name == "quietchat" {
			m.form.toolIndex = i
		}
	}
	pickGroup(t, m, "")
	_, cmd := m.submitForm()
	// A create lands inside the session it made; this test is about the board,
	// so it steps back out.
	if m.mode != modeFocus {
		t.Fatalf("after submit, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	m.applyCmd(t, cmd)
	m.leaveFocusForFixture(t)
	sess := m.sessionRows()[0]

	send := func(text string) {
		t.Helper()
		if err := m.tmux.SendText(sess.ID, text); err != nil {
			t.Fatalf("send %q: %v", text, err)
		}
	}
	waitStatus := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			m.applyCmd(t, m.refreshCmd())
			got, err := m.store.Get(sess.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Status == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("status = %q, want %q", got.Status, want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	send("first answer chunk")
	send("› ask anything")
	m.applyCmd(t, m.refreshCmd())

	send("more streaming output")
	send("› ask anything")
	waitStatus(status.Working)
	waitStatus(status.Finished)
}

func writePendingName(t *testing.T, m *Model, id, name string) {
	t.Helper()
	path := m.hooks.NameFile(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
		t.Fatalf("write name: %v", err)
	}
}

func TestRefreshAppliesPendingRename(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "placeholder", t.TempDir(), "")
	sess := m.sessionRows()[0]

	writePendingName(t, m, sess.ID, "fix auth bug\n")
	m.applyCmd(t, m.refreshCmd())

	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "fix auth bug" {
		t.Fatalf("name = %q, want the agent-chosen name", got.Name)
	}
	if _, err := os.Stat(m.hooks.NameFile(sess.ID)); !os.IsNotExist(err) {
		t.Fatal("applied name file should be consumed")
	}
}

func TestRefreshConsumesGarbageNameFile(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "keeper", t.TempDir(), "")
	sess := m.sessionRows()[0]

	writePendingName(t, m, sess.ID, "   \n")
	m.applyCmd(t, m.refreshCmd())

	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "keeper" {
		t.Fatalf("whitespace name must not rename, got %q", got.Name)
	}
	if _, err := os.Stat(m.hooks.NameFile(sess.ID)); !os.IsNotExist(err) {
		t.Fatal("garbage name file should still be consumed")
	}
}

func TestRefreshWithStaleSelectionFetchesPreview(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "fresh-one", t.TempDir(), "")
	m.selectSessionRow(t, "fresh-one")
	sess := m.sessionRows()[0]

	_, cmd := m.Update(refreshMsg{sessions: m.sessions, procFor: ""})
	if cmd == nil {
		t.Fatal("stale refresh should schedule an immediate preview fetch")
	}
	if m.poller.selectedID != sess.ID {
		t.Fatalf("poller selectedID = %q want %q", m.poller.selectedID, sess.ID)
	}

	m.preview = "existing"
	if _, cmd := m.Update(refreshMsg{sessions: m.sessions, procFor: sess.ID, preview: "pane text"}); cmd != nil {
		t.Fatal("matching refresh should not schedule extra work")
	}
	if m.preview != "pane text" {
		t.Fatalf("preview = %q want %q", m.preview, "pane text")
	}
}

func TestSweepPastesReportsSweepError(t *testing.T) {
	orig := sweepStalePastes
	defer func() { sweepStalePastes = orig }()
	sweepStalePastes = func() error { return errors.New("permission denied") }

	m := &Model{}
	msg, ok := m.sweepPastes().(pasteSweepMsg)
	if !ok {
		t.Fatalf("want pasteSweepMsg, got %T", m.sweepPastes())
	}
	if msg.err == nil || msg.err.Error() != "permission denied" {
		t.Fatalf("got %v", msg.err)
	}
}

func TestPasteSweepMsgSurfacesErrorOnce(t *testing.T) {
	m := buildModel(t)
	m.Update(pasteSweepMsg{err: errors.New("permission denied")})
	if m.errBar.text == "" {
		t.Fatal("a failed sweep must reach the user")
	}
	m.errBar.text = ""
	m.Update(pasteSweepMsg{})
	if m.errBar.text != "" {
		t.Fatalf("a clean sweep must stay silent, got %q", m.errBar.text)
	}
}

func TestPasteSweepTickSweepsAgainAndRearms(t *testing.T) {
	m := buildModel(t)
	_, cmd := m.Update(pasteSweepTickMsg{})
	if cmd == nil {
		t.Fatal("tick must return work")
	}
	// A manager left open for weeks only keeps sweeping if the tick both
	// sweeps and re-arms, so the batch must carry two commands. Running the
	// timer itself here would wait out the real interval.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("want a batch, got %T", cmd())
	}
	if len(batch) != 2 {
		t.Fatalf("want sweep plus re-arm, got %d commands", len(batch))
	}
}

func writeCodexRollout(t *testing.T, path, sessionID, cwd string, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"session_meta","payload":{"session_id":"` + sessionID + `","cwd":"` + cwd + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

// Two codex sessions share a directory, A launched a fraction of a second
// before B, both within the same wall-clock second. The poller receives
// them in store order (B first), not launch order. Sub-second launch times
// must survive the store round-trip so capture binds each to its own
// conversation instead of swapping them.
func TestCaptureAgentSessionIDsAssignsInLaunchOrder(t *testing.T) {
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()

	// A whole second, so A and B share it and only nanoseconds separate them.
	base := time.Now().Truncate(time.Second).Add(-time.Minute)
	aLaunch := base.Add(100 * time.Millisecond)
	bLaunch := base.Add(600 * time.Millisecond)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-A.jsonl"), "A-id", cwd, aLaunch)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-B.jsonl"), "B-id", cwd, bLaunch)

	if err := st.CreateSession(store.Session{ID: "sess-A", Name: "a", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: aLaunch}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(store.Session{ID: "sess-B", Name: "b", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: bLaunch}); err != nil {
		t.Fatal(err)
	}
	sessA, err := st.Get("sess-A")
	if err != nil {
		t.Fatal(err)
	}
	sessB, err := st.Get("sess-B")
	if err != nil {
		t.Fatal(err)
	}

	sessions := []store.Session{sessB, sessA} // store order, not launch order
	p := &poller{store: st, sessionStores: map[string]string{"codex": "codex"}}
	panes := map[string]int{"sess-A": 123, "sess-B": 456}
	captured, err := p.captureAgentSessionIDs(sessions, panes)
	if err != nil {
		t.Fatal(err)
	}
	if captured != 2 {
		t.Fatalf("captured %d, want 2", captured)
	}

	gotA, err := st.Get("sess-A")
	if err != nil {
		t.Fatal(err)
	}
	if gotA.AgentSessionID != "A-id" {
		t.Fatalf("session A captured %q, want A-id", gotA.AgentSessionID)
	}
	gotB, err := st.Get("sess-B")
	if err != nil {
		t.Fatal(err)
	}
	if gotB.AgentSessionID != "B-id" {
		t.Fatalf("session B captured %q, want B-id", gotB.AgentSessionID)
	}
}

// A restarted codex session still has its old rollout sitting in the same
// directory, written moments before the restart and so inside the capture
// window. Capture must bind the fresh conversation, not walk the session
// straight back into the context the restart dropped.
func TestCaptureAgentSessionIDsSkipsARetiredConversation(t *testing.T) {
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()

	created := time.Now().Add(-time.Hour)
	restarted := time.Now().Add(-time.Second)
	// The retired rollout's last write lands two seconds before the restart,
	// inside the clock slack the capture window allows.
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-old.jsonl"), "old-id", cwd, restarted.Add(-2*time.Second))
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-new.jsonl"), "new-id", cwd, restarted.Add(time.Second))

	if err := st.CreateSession(store.Session{ID: "sess", Name: "s", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: created, AgentSessionID: "old-id"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RestartAgent("sess", "", restarted); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Get("sess")
	if err != nil {
		t.Fatal(err)
	}

	p := &poller{store: st, sessionStores: map[string]string{"codex": "codex"}}
	if _, err := p.captureAgentSessionIDs([]store.Session{sess}, map[string]int{"sess": 42}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get("sess")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentSessionID != "new-id" {
		t.Fatalf("captured %q, want new-id", got.AgentSessionID)
	}
}

// Capture reads a tool's session store from a snapshot and can take minutes,
// so a restart can land while a pass is still looking. The answer it comes
// back with names the conversation the restart dropped, and writing it would
// walk the row straight back into the context the user just left.
func TestCaptureAgentSessionIDsDropsAnAnswerARestartOutran(t *testing.T) {
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	cwd := t.TempDir()
	created := time.Now().Add(-time.Hour)
	writeCodexRollout(t, filepath.Join(codexHome, "sessions", "rollout-old.jsonl"), "old-id", cwd, created)

	if err := st.CreateSession(store.Session{ID: "sess", Name: "s", Tool: "codex", Cwd: cwd, Group: "g", Status: "idle", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	// The pass reads the session before the restart: no id yet, so it is a
	// capture candidate and the old rollout is the answer it will find.
	snapshot, err := st.Get("sess")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RestartAgent("sess", "", time.Now()); err != nil {
		t.Fatal(err)
	}

	p := &poller{store: st, sessionStores: map[string]string{"codex": "codex"}}
	captured, err := p.captureAgentSessionIDs([]store.Session{snapshot}, map[string]int{"sess": 7})
	if err != nil {
		t.Fatal(err)
	}
	if captured != 0 {
		t.Fatalf("captured %d, want the stale answer dropped", captured)
	}
	got, err := st.Get("sess")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentSessionID != "" {
		t.Fatalf("restarted session bound to %q, want it left for the next pass", got.AgentSessionID)
	}
}

// Taking the launch prompt clears the composer, so a directive delivered
// before then is discarded and has to wait for the prompt to reach output.
//
// Quarantined: this fails about four runs in five on a pristine main, and did
// so long before there was any CI to notice. It asserts that something has
// not happened yet -- that the directive is still pending -- across three
// fixed 50ms tries, while slow-take-tool prints its first "❯ " immediately
// and the activity cutoff "(?m)^❯" matches that first prompt. Whether the
// poll lands before or after the pane has drawn one character decides the
// run, so no wait fixes it: the fixture needs a point the test can hold the
// prompt at. Left to the owner of the launch-prompt path rather than
// reshaped from outside it, and named in ci-allowed-skips.txt so the gap is
// counted rather than silent.
func TestPendingInputWaitsForTheLaunchPrompt(t *testing.T) {
	t.Skip("races the pane's first prompt: fails ~4 runs in 5 on main, see above")
	m := buildModel(t)
	if err := m.spawnSession("slow-take-tool", "slow-take-tool-abcd", t.TempDir(), "", "/compact", true); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	sess := m.sessionRows()[0]

	// The input line is drawn from the first frame, so without the wait the
	// directive would be gone by now.
	for tries := 0; tries < 3; tries++ {
		if !sessionHasPendingInput(t, m, sess.ID, launch.DeferredRenameDirective) {
			pane, _ := m.tmux.CapturePane(sess.ID)
			t.Fatalf("directive sent before the prompt was taken; pane:\n%s", pane)
		}
		time.Sleep(50 * time.Millisecond)
		m.applyCmd(t, m.refreshCmd())
	}

	deadline := time.Now().Add(5 * time.Second)
	for sessionHasPendingInput(t, m, sess.ID, launch.DeferredRenameDirective) {
		if time.Now().After(deadline) {
			pane, _ := m.tmux.CapturePane(sess.ID)
			t.Fatalf("directive never sent after the prompt was taken; pane:\n%s", pane)
		}
		time.Sleep(100 * time.Millisecond)
		m.applyCmd(t, m.refreshCmd())
	}
	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !strings.Contains(pane, "GATE_INBOX_BIN") {
		t.Fatalf("pane should hold the directive, got:\n%s", pane)
	}
}

// A prompt that never reaches the pane, because it scrolled out or the agent
// never drew it, must not hold pending input past the grace.
func TestLaunchPromptTakenGivesUpAfterTheGrace(t *testing.T) {
	fresh := store.Session{LaunchPrompt: "/compact plan the sprint", CreatedAt: time.Now()}
	if launchPromptTaken(fresh, "no prompt here") {
		t.Fatal("pending input released before the prompt showed")
	}
	if !launchPromptTaken(fresh, "❯ /compact plan the sprint\nworking") {
		t.Fatal("prompt in the region should release pending input")
	}
	stale := store.Session{LaunchPrompt: fresh.LaunchPrompt, CreatedAt: time.Now().Add(-launchPromptGrace - time.Second)}
	if !launchPromptTaken(stale, "no prompt here") {
		t.Fatal("pending input still held after the grace")
	}
}

// An adopted row cannot be revived — Create refuses a pane the manager did
// not start — so once the pane is gone the row is a tombstone that only a
// manual delete clears. The poll pass that proves the pane gone drops it.
func TestPollDropsAnAdoptedRowOnceItsPaneIsGone(t *testing.T) {
	m := buildModel(t)
	id, socket, pane := adoptedRow(t, m, "borrowed", "")
	// A second pane keeps the foreign server up once the adopted one is
	// killed, so this is the "that server answered and your pane was not on
	// it" case rather than the server disappearing wholesale.
	if out, err := exec.Command("tmux", "-L", socket, "split-window", "-t", "user", "-c", "/tmp", "cat").CombinedOutput(); err != nil {
		t.Fatalf("second foreign pane: %v: %s", err, out)
	}
	if out, err := exec.Command("tmux", "-L", socket, "kill-pane", "-t", pane).CombinedOutput(); err != nil {
		t.Fatalf("kill the adopted pane: %v: %s", err, out)
	}

	m.applyCmd(t, m.refreshCmd())
	if _, err := m.store.Get(id); err != nil {
		t.Fatalf("one look is not proof enough to delete on: %v", err)
	}

	m.applyCmd(t, m.refreshCmd())
	if _, err := m.store.Get(id); err == nil {
		t.Fatal("the adopted row should be gone from the store")
	}
	if !sessionGone(m.sessions, id) {
		t.Fatalf("the adopted row should be gone from the board, got %+v", m.sessions)
	}
	if _, adopted := m.tmux.AdoptedTarget(id); adopted {
		t.Fatal("deleting the row must release the driver's adoption entry")
	}
}

// The managed half is untouched: a session the manager started keeps its dead
// row, which holds the name, group and history revive puts an agent back
// into.
func TestPollKeepsTheDeadRowOfAManagedSession(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "mine", t.TempDir(), "")
	rows := m.sessionRows()
	if len(rows) != 1 {
		t.Fatalf("want one session row, got %+v", rows)
	}
	id := rows[0].ID
	if err := m.tmux.Kill(id); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	for pass := 0; pass < 3; pass++ {
		m.applyCmd(t, m.refreshCmd())
	}
	stored, err := m.store.Get(id)
	if err != nil {
		t.Fatalf("a managed session keeps its row: %v", err)
	}
	if stored.Status != status.Dead {
		t.Fatalf("status = %q, want %q", stored.Status, status.Dead)
	}
	if sessionGone(m.sessions, id) {
		t.Fatalf("the dead row should still be on the board, got %+v", m.sessions)
	}
	m.selectSessionRow(t, "mine")
	_, _ = m.reviveSelected()
	if m.errBar.text != "" {
		t.Fatalf("revive: %q", m.errBar.text)
	}
	m.applyCmd(t, nil)
	if !m.tmux.Exists(id) {
		t.Fatal("the dead row should still be revivable")
	}
}

// The failure this whole design exists for: a foreign server that cannot be
// listed. Its panes are absent from the liveness map exactly as a closed
// pane is, and deleting on that would clear the board on one bad tmux call.
// Proven by making the listing fail, not by reasoning about it.
func TestPollKeepsAdoptedRowsWhenTheServerCannotBeListed(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	// The shim starts as a pass-through so the server can be built and
	// adopted, and breaks the moment the marker exists.
	broken := filepath.Join(dir, "broken")
	shim := filepath.Join(dir, "tmux")
	// The foreign socket carries a per-run suffix, so the shim can only be
	// written once the server it must refuse actually exists -- a literal
	// name here silently stops matching and the guard loses its teeth.
	writeShim := func(socket string) {
		t.Helper()
		script := "#!/bin/sh\nif [ -f " + broken + " ]; then\n" +
			"  case \"$*\" in \"-L " + socket + " list-panes\"*)\n" +
			"    echo 'tmux: server exited unexpectedly' >&2; exit 1;;\n  esac\nfi\n" +
			"exec " + realTmux + " \"$@\"\n"
		if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
			t.Fatalf("shim: %v", err)
		}
	}
	writeShim("unset")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := buildModel(t)
	id, socket, pane := adoptedRow(t, m, "borrowed", "")
	writeShim(socket)
	if err := os.WriteFile(broken, nil, 0o600); err != nil {
		t.Fatalf("break the listing: %v", err)
	}

	for pass := 0; pass < 3; pass++ {
		msg := m.refreshCmd()()
		failed, ok := msg.(errMsg)
		if !ok {
			t.Fatalf("pass %d should have failed on the unreadable server, got %T", pass, msg)
		}
		if !strings.Contains(failed.err.Error(), "list-panes") {
			t.Fatalf("pass %d failed for the wrong reason: %v", pass, failed.err)
		}
	}
	if _, err := m.store.Get(id); err != nil {
		t.Fatalf("a server that could not be listed proves nothing: %v", err)
	}
	if _, adopted := m.tmux.AdoptedTarget(id); !adopted {
		t.Fatal("the driver should still be driving the pane")
	}
	out, err := exec.Command(realTmux, "-L", socket, "list-panes", "-a", "-F", "#{pane_id}").CombinedOutput()
	if err != nil || !strings.Contains(string(out), pane) {
		t.Fatalf("the pane was alive the whole time: %v: %s", err, out)
	}
}

// The same distinction one level down from the pane listing: the whole board
// now reads its panes over one control-mode client per tmux server, so a
// server that stops answering costs every session on it its capture at once.
// A missing capture is silence, not proof, and a pass that read it as proof
// would mark a shelf of live agents dead and alert on every one of them.
func TestPollKeepsTheStatusOfASessionWhoseCaptureFailed(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken")
	shim := filepath.Join(dir, "tmux")
	// Reads of the pane keep failing while the pane listing keeps working:
	// tmux says the pane is there and refuses to say what is in it.
	writeShim := func(socket string) {
		t.Helper()
		script := "#!/bin/sh\nif [ -f " + broken + " ]; then\n" +
			"  case \"$*\" in\n" +
			"    \"-L " + socket + " -C \"*|\"-L " + socket + " capture-pane\"*)\n" +
			"      echo 'tmux: server exited unexpectedly' >&2; exit 1;;\n" +
			"  esac\nfi\n" +
			"exec " + realTmux + " \"$@\"\n"
		if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
			t.Fatalf("shim: %v", err)
		}
	}
	writeShim("unset")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := buildModel(t)
	id, socket, _ := adoptedRow(t, m, "borrowed", "")
	if err := m.store.UpdateStatus(id, status.Working); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	// New() puts the opencode v1 warning on the status bar when this
	// machine runs v1. This test asserts the refresh passes write no
	// errors, so the startup notice is cleared first: what matters is what
	// the passes below write, not what construction said.
	m.errBar.text, m.errBar.done = "", ""
	writeShim(socket)
	if err := os.WriteFile(broken, nil, 0o600); err != nil {
		t.Fatalf("break the capture: %v", err)
	}
	// The client already open on that server would keep answering, which is
	// the point of holding it; drop it so the pass has to face the failure.
	m.tmux.CloseCaptureClients()

	for pass := 0; pass < 3; pass++ {
		m.applyCmd(t, m.refreshCmd())
		if m.errBar.text != "" {
			t.Fatalf("pass %d: %s", pass, m.errBar.text)
		}
		stored, err := m.store.Get(id)
		if err != nil {
			t.Fatalf("pass %d: the row should still be there: %v", pass, err)
		}
		if stored.Status != status.Working {
			t.Fatalf("pass %d: a pane that could not be read reported %q, want %q",
				pass, stored.Status, status.Working)
		}
	}
}

// jumpToBottom is the affordance claude draws over the last content row
// while its own viewport is parked above the live bottom. It is written
// out here exactly as a real capture carries it.
const jumpToBottom = "                    Jump to bottom (ctrl+End) ↓"

// useShippedRules swaps the fixture tools for the ones the program ships,
// so a test reads the same patterns an operator's board does.
func useShippedRules(t *testing.T, m *Model) {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		t.Fatalf("status engine: %v", err)
	}
	m.poller.engine = engine
}

// pass runs one poll the way refreshOnce does: a fresh hash map per pass,
// promoted to the baseline the next pass compares against.
func pass(t *testing.T, m *Model, sess store.Session, pane string) string {
	t.Helper()
	hashes := map[string]uint64{}
	got, err := m.poller.derivePaneStatus(sess, pane, true, hashes)
	if err != nil {
		t.Fatalf("derivePaneStatus: %v", err)
	}
	m.poller.paneHashes = hashes
	return got
}

// An agent that scrolls its own viewport puts history on the visible
// screen, and a spinner two hundred lines up belongs to a turn that ended
// long ago. Matching it reported a finished session as working for as long
// as the operator kept reading, which is what a real claude does here:
// this pane is the shape a live capture takes.
func TestDisplacedViewportIgnoresAStaleSpinner(t *testing.T) {
	m := buildModel(t)
	useShippedRules(t, m)
	sess := store.Session{ID: "scroll10", Tool: "claude", Status: status.Idle}
	live := "✻ Cogitated for 2s · done 4:44 AM\n❯ \n"
	history := "● - Drizzling… (6s · thinking)\n    esc to interrupt\n    1\n    2"

	seedRegionHash(t, m, sess, live)
	if got := deriveStatus(t, m, sess, history+"\n❯ \n", true); got != status.Working {
		t.Fatalf("the same screen without the affordance reads working (precondition), got %q", got)
	}
	seedRegionHash(t, m, sess, live)
	if got := deriveStatus(t, m, sess, history+jumpToBottom+"\n❯ \n", true); got != status.Idle {
		t.Fatalf("a spinner scrolled up from a finished turn must not read working, got %q", got)
	}
}

// The same screen, the same stale text, read through the hook file rather
// than the rules: hooks keep reporting what the agent is doing, and it is
// only the pane cross-check on top of them that the scroll invalidates.
func TestDisplacedViewportDoesNotFlipTheHookVerdict(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "scroll11", Tool: "claude-hooked", Status: status.Finished}
	writeHookStatus(t, m, sess.ID, status.Finished)
	dialog := "Choose one\n  Enter to confirm · Esc to cancel"

	if got := deriveStatus(t, m, sess, dialog+"\n❯ \n", true); got != status.Waiting {
		t.Fatalf("a live dialog upgrades a finished hook to waiting (precondition), got %q", got)
	}
	if got := deriveStatus(t, m, sess, dialog+jumpToBottom+"\n❯ \n", true); got != status.Finished {
		t.Fatalf("a dialog scrolled up from an answered turn must not read waiting, got %q", got)
	}
}

// Holding must not swallow the turn end itself. A marker-less turn that
// finishes while the operator reads history resolves as soon as the
// viewport is back: the displaced polls record no baseline, so the first
// pass at the live bottom rebaselines and the next one settles it.
func TestTurnEndingWhileDisplacedResolvesOnReturn(t *testing.T) {
	m := buildModel(t)
	disableQuietEndGrace(t)
	sess := store.Session{ID: "scroll12", Tool: "claude-hooked", Status: status.Working}
	seedRegionHash(t, m, sess, "answer line one\n❯ \n")

	for _, scrolled := range []string{
		"older line\nolder line two" + jumpToBottom + "\n❯ \n",
		"older line\nolder line three" + jumpToBottom + "\n❯ \n",
	} {
		if got := pass(t, m, sess, scrolled); got != status.Working {
			t.Fatalf("a displaced viewport holds the in-flight turn, got %q", got)
		}
	}
	final := "answer line one\nanswer line two\n❯ \n"
	if got := pass(t, m, sess, final); got != status.Working {
		t.Fatalf("the first pass back at the live bottom only rebaselines, got %q", got)
	}
	if got := pass(t, m, sess, final); got != status.Finished {
		t.Fatalf("a turn that ended while displaced must resolve on return, got %q", got)
	}
}

// The heuristic's real purpose survives: streaming with no spinner at all
// is still what turns a quiet row into a working one, both before a scroll
// and after the viewport comes back.
func TestDisplacedViewportStillSeesStreamingAtTheLiveBottom(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "scroll13", Tool: "claude-hooked", Status: status.Idle}
	seedRegionHash(t, m, sess, "line one\n❯ \n")
	if got := pass(t, m, sess, "older line"+jumpToBottom+"\n❯ \n"); got != status.Idle {
		t.Fatalf("scrolling an idle session must leave it idle, got %q", got)
	}
	if got := pass(t, m, sess, "line one\n❯ \n"); got != status.Idle {
		t.Fatalf("the rebaseline pass must not invent working, got %q", got)
	}
	if got := pass(t, m, sess, "line one\nstreaming text\n❯ \n"); got != status.Working {
		t.Fatalf("streaming after the viewport returns must derive working, got %q", got)
	}
}

// growingBoxPane is the shape a real capture takes: a pane full of
// conversation, the tool's input box pinned to the bottom. Writing a prompt
// grows the box downward, and on a full pane every extra row it takes
// scrolls one of the oldest rows off the top of the capture. Nothing the
// agent wrote changed.
func growingBoxPane(boxRows int) string {
	const paneRows, lastLine = 24, 60
	var b strings.Builder
	// The newest conversation line stays put against the box; the oldest
	// visible one is whatever the shrunken conversation area still reaches.
	for i := lastLine - (paneRows - boxRows) + 1; i <= lastLine; i++ {
		fmt.Fprintf(&b, "conversation line %d\n", i)
	}
	b.WriteString("❯ a prompt someone is part way through writing\n")
	for i := 1; i < boxRows; i++ {
		b.WriteString("  and it wrapped onto another row\n")
	}
	return b.String()
}

// The flop this closes: focusing a session and typing into it flipped the
// row to working before Enter was ever pressed, because the region hash
// covered rows the growing box had pushed off the top.
func TestAGrowingInputBoxIsNotReadAsAgentOutput(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "typing01", Tool: "claude-hooked", Status: status.Idle}
	seedRegionHash(t, m, sess, growingBoxPane(1))
	for _, rows := range []int{2, 3, 6} {
		if got := deriveStatus(t, m, sess, growingBoxPane(rows), true); got != status.Idle {
			t.Fatalf("a prompt grown to %d rows must leave the session idle, got %q", rows, got)
		}
	}
}

// The narrower fingerprint must not go blind: output still lands at the
// bottom of the region, and it counts even on the pass where the box grew.
func TestStreamingUnderAGrowingBoxStillReadsAsWorking(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "typing02", Tool: "claude-hooked", Status: status.Idle}
	seedRegionHash(t, m, sess, growingBoxPane(1))
	grown := strings.Replace(growingBoxPane(3), "❯ ", "the agent answered\n❯ ", 1)
	if got := deriveStatus(t, m, sess, grown, true); got != status.Working {
		t.Fatalf("a new line above the box is the agent working, got %q", got)
	}
}

// admin has been seen at seconds on a live board while every phase that
// touches a pane came in under a tenth of one. The things it is made of are
// unrelated, so the total alone cannot say which of them to go after: the
// parts have to land in the log line beside it, and a pass that skipped the
// conditional one has to read as skipped rather than as instant.
func TestThePollAccountsForWhatAdminSpent(t *testing.T) {
	m := buildModel(t)
	m.applyCmd(t, m.refreshCmd())

	var pass passStat
	m.poller.refreshPass(&pass)
	if pass.phases.mu <= 0 {
		t.Fatal("the pass charged nothing to the mutex it took")
	}
	if sum := pass.phases.mu + pass.phases.prune; pass.phases.admin < sum {
		t.Fatalf("admin = %v, less than the %v its parts account for", pass.phases.admin, sum)
	}
	if pass.phases.prune != 0 {
		t.Fatalf("a pass between sweeps charged %v to the prune it skipped", pass.phases.prune)
	}

	var quiet passStat
	m.poller.refreshPass(&quiet)

	rendered := map[string]bool{}
	fields := quiet.phases.fields()
	for i := 0; i < len(fields); i += 2 {
		rendered[fields[i].(string)] = true
	}
	for _, name := range []string{"admin", "admin.mu", "admin.prune"} {
		if !rendered[name] {
			t.Fatalf("%s is missing from the pass line the breakdown is read from", name)
		}
	}
}

// declarePriority is a session writing its own tier through the priority
// subcommand or its MCP tool, which both land in the same mailbox.
func declarePriority(t *testing.T, m *Model, id, tier string) {
	t.Helper()
	path := m.hooks.PriorityFile(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("hooks dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(tier), 0o644); err != nil {
		t.Fatalf("write priority file: %v", err)
	}
}

// A session that declares its own tier gets it, and the mailbox is
// consumed rather than re-read on every poll for the rest of its life.
func TestPendingPriorityIsAppliedAndConsumed(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	sess := m.sessionRows()[0]

	declarePriority(t, m, sess.ID, "urgent")
	if err := m.poller.applyPendingPriority(&sess); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if sess.Priority != priority.Urgent {
		t.Fatalf("the poll left the row at %q", sess.Priority)
	}
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Priority != priority.Urgent {
		t.Fatalf("the store holds %q", stored.Priority)
	}
	if _, found := m.hooks.ReadPriority(sess.ID); found {
		t.Fatal("the mailbox should be consumed instead of retried every poll")
	}
}

// A tier the session spelled wrong is dropped, not retried: the file is
// written by an agent, and re-reading the same bad word on every poll is
// how one typo becomes a permanent log.
func TestPendingPriorityDropsAnUnreadableTier(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	sess := m.sessionRows()[0]
	if err := m.store.SetPriority(sess.ID, priority.High); err != nil {
		t.Fatal(err)
	}
	sess.Priority = priority.High

	declarePriority(t, m, sess.ID, "p0")
	if err := m.poller.applyPendingPriority(&sess); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if sess.Priority != priority.High {
		t.Fatalf("a bad declaration changed the tier to %q", sess.Priority)
	}
	if _, found := m.hooks.ReadPriority(sess.ID); found {
		t.Fatal("a bad declaration is retried on every poll")
	}
}

// A row deleted mid-pass must not fail the poll, the same as a rename.
func TestPendingPriorityOfADeletedSessionDoesNotFailThePass(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	sess := m.sessionRows()[0]
	if err := m.store.Delete(sess.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	declarePriority(t, m, sess.ID, "urgent")
	if err := m.poller.applyPendingPriority(&sess); err != nil {
		t.Fatalf("priority of a deleted session should not fail the pass: %v", err)
	}
	if _, found := m.hooks.ReadPriority(sess.ID); found {
		t.Fatal("the mailbox should be consumed instead of retried every poll")
	}
}
