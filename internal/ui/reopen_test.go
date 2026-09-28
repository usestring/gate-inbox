package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// longAgo is a launch from before any tmux server a test starts, which is
// what a session the machine lost in a reboot looks like.
var longAgo = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// lostSession puts a row on the board whose agent launched on a server that
// has since gone: dead, never ended on purpose.
func lostSession(t *testing.T, m *Model, id string) {
	t.Helper()
	if err := m.store.CreateSession(store.Session{ID: id, Name: id, Tool: "claude", Cwd: t.TempDir(),
		Status: status.Dead, CreatedAt: longAgo, LastStatusAt: longAgo}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := m.store.SetAgentLaunchedAt(id, longAgo); err != nil {
		t.Fatalf("SetAgentLaunchedAt: %v", err)
	}
}

// reopening is a board starting up: armed, with the first adopt scan done.
func reopening(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	m.restoreArmed = true
	m.adoptFirstDone = true
	return m
}

func setMode(t *testing.T, m *Model, key, value string) {
	t.Helper()
	if err := m.store.SetSetting(key, value); err != nil {
		t.Fatalf("SetSetting %s: %v", key, err)
	}
}

func TestReopenAsksOnlyAboutLostSessions(t *testing.T) {
	m := reopening(t)
	lostSession(t, m, "lost")
	socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)

	m.applyCmd(t, nil)
	if m.mode != modeRestorePrompt {
		t.Fatalf("mode = %v, want the reopen card", m.mode)
	}
	if len(m.restore.candidates) != 1 {
		t.Fatalf("card holds %d sessions, want only the lost session", len(m.restore.candidates))
	}
	out := ansi.Strip(m.frame())
	for _, want := range []string{"Welcome back", "1 session stopped without you ending it", "rebooted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("card missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "outside the board") {
		t.Fatalf("card still asks about the outside pane:\n%s", out)
	}
	pressKey(t, m, key("y"))
	if m.mode != modeList {
		t.Fatalf("after y mode = %v", m.mode)
	}
	if !m.tmux.Exists("lost") {
		t.Fatal("the lost session was not resumed")
	}
	if !foreignPaneAlive(t, socket, pane) {
		t.Fatal("the outside pane was ended")
	}
	if _, err := m.store.Get("byhand"); err == nil {
		t.Fatal("the outside pane is still on the board")
	}
}

// A session the operator ended is not on the card, even across a reboot.
func TestReopenLeavesOutASessionTheOperatorKilled(t *testing.T) {
	m := reopening(t)
	lostSession(t, m, "killed")
	row, _ := m.store.Get("killed")
	if err := m.store.RecordEnd(row, store.EndKilled); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)
	if m.mode != modeList {
		t.Fatalf("a killed session raised the card: %+v", m.restore.candidates)
	}
}

func TestEachReopenSessionsSettingValue(t *testing.T) {
	t.Run("ask", func(t *testing.T) {
		m := reopening(t)
		lostSession(t, m, "lost")
		m.applyCmd(t, nil)
		if m.mode != modeRestorePrompt {
			t.Fatalf("ask should raise the card, mode = %v", m.mode)
		}
	})
	t.Run("resume", func(t *testing.T) {
		m := reopening(t)
		setMode(t, m, reopenSessionsSetting, reopenResume)
		lostSession(t, m, "lost")
		m.applyCmd(t, nil)
		if m.mode != modeList {
			t.Fatalf("resume should not ask, mode = %v", m.mode)
		}
		if !m.tmux.Exists("lost") {
			t.Fatal("resume should bring the lost session back")
		}
		if !strings.Contains(m.errBar.text, "resumed 1 session") || !strings.Contains(m.errBar.text, "settings") {
			t.Fatalf("notice = %q", m.errBar.text)
		}
	})
	t.Run("never", func(t *testing.T) {
		m := reopening(t)
		setMode(t, m, reopenSessionsSetting, reopenNever)
		lostSession(t, m, "lost")
		m.applyCmd(t, nil)
		if m.mode != modeList || m.tmux.Exists("lost") {
			t.Fatalf("never should neither ask nor resume, mode = %v", m.mode)
		}
		if !strings.Contains(m.errBar.text, "V revives") {
			t.Fatalf("notice = %q, want one line pointing at V", m.errBar.text)
		}
	})
}

func TestEachOutsidePanesSettingValue(t *testing.T) {
	for _, legacy := range []string{"", reopenAsk, paneAdopt, "unknown"} {
		t.Run("legacy-"+legacy, func(t *testing.T) {
			m := reopening(t)
			setMode(t, m, outsidePanesSetting, legacy)
			socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
			if legacy == paneAdopt {
				m.applyPaneChoices(m.outsidePaneCandidates(), func(store.Session) string { return paneAdopt })
			}
			m.applyCmd(t, nil)
			if m.mode != modeList {
				t.Fatalf("legacy setting should not ask, mode = %v", m.mode)
			}
			if _, err := m.store.Get("byhand"); err == nil || !foreignPaneAlive(t, socket, pane) {
				t.Fatal("legacy setting should leave the pane running off the board")
			}
		})
	}
	t.Run("relaunch", func(t *testing.T) {
		m := reopening(t)
		setMode(t, m, outsidePanesSetting, paneRelaunch)
		socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
		m.applyCmd(t, nil)
		if m.mode != modeList {
			t.Fatalf("relaunch should not ask, mode = %v", m.mode)
		}
		if foreignPaneAlive(t, socket, pane) || !m.tmux.Exists("byhand") {
			t.Fatal("relaunch should move the idle pane into the board")
		}
		if !strings.Contains(m.errBar.text, "relaunching into the board") {
			t.Fatalf("notice = %q", m.errBar.text)
		}
	})
	t.Run("ignore", func(t *testing.T) {
		m := reopening(t)
		setMode(t, m, outsidePanesSetting, paneIgnore)
		socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
		m.applyCmd(t, nil)
		if m.mode != modeList {
			t.Fatalf("ignore should not ask, mode = %v", m.mode)
		}
		if _, err := m.store.Get("byhand"); err == nil {
			t.Fatal("ignore should take the row off the board")
		}
		if !foreignPaneAlive(t, socket, pane) {
			t.Fatal("ignore must leave the pane itself running")
		}
		if !strings.Contains(m.errBar.text, "left off the board") {
			t.Fatalf("notice = %q", m.errBar.text)
		}
	})
}

// "Never ask again" applies the answer on screen and stores it as the default
// for both halves, and says where to turn the question back on.
func TestNeverAskStoresTheAnswerAsTheDefault(t *testing.T) {
	m := reopening(t)
	lostSession(t, m, "lost")
	adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
	m.applyCmd(t, nil)
	if m.mode != modeRestorePrompt {
		t.Fatalf("mode = %v", m.mode)
	}
	pressKey(t, m, key("N"))
	if m.mode != modeList {
		t.Fatalf("after N mode = %v", m.mode)
	}
	if got := m.reopenSessionsMode(); got != reopenResume {
		t.Fatalf("on reopen = %q, want resume", got)
	}
	if got := m.outsidePanesMode(); got != paneIgnore {
		t.Fatalf("outside panes = %q, want ignore", got)
	}
	if !m.tmux.Exists("lost") {
		t.Fatal("N should still apply the answer it stores")
	}
	if !strings.Contains(m.errBar.text, "won't ask again") || !strings.Contains(m.errBar.text, "settings") {
		t.Fatalf("notice = %q", m.errBar.text)
	}
}

func TestIgnoreRemembersALeftOutPane(t *testing.T) {
	m := buildModel(t)
	leftSocket, leftPane := adoptForeignPane(t, m, "left", "left", status.Idle)
	m.applyCmd(t, nil)
	m.applyPaneChoices(m.outsidePaneCandidates(), func(store.Session) string { return paneIgnore })
	if _, err := m.store.Get("left"); err == nil {
		t.Fatal("left should be off the board")
	}
	if !foreignPaneAlive(t, leftSocket, leftPane) {
		t.Fatal("leaving a pane out must not end it")
	}
	// The next scan sees the pane and leaves it alone.
	run := &adoptRun{
		tools:    m.adoptTools(),
		known:    map[string]bool{},
		onBoard:  map[string]bool{},
		names:    map[string]bool{},
		stor:     m.store,
		driver:   m.tmux,
		rejected: map[string]int{},
		ignored:  loadPaneDecisions(m.store).ignoredPaneKeys(),
	}
	taken, err := run.take(adopt.Panes(leftSocket), adopt.NewProcTable())
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if taken != 0 || run.rejected["left off the board by the operator"] != 1 {
		t.Fatalf("the scan took %d, rejections %v; want the left-out pane refused", taken, run.rejected)
	}
}

// With outside panes set to be ignored, the scan takes none of them at all.
func TestTheIgnoreSettingKeepsTheScanOffOutsidePanes(t *testing.T) {
	m := buildModel(t)
	socket, _ := uiForeignServer(t, "cat")
	run := &adoptRun{
		tools: m.adoptTools(), known: map[string]bool{}, onBoard: map[string]bool{}, names: map[string]bool{},
		stor: m.store, driver: m.tmux, rejected: map[string]int{}, skipForeign: true,
	}
	taken, err := run.take(adopt.Panes(socket), adopt.NewProcTable())
	if err != nil || taken != 0 || run.rejected["outside panes are set to be ignored"] == 0 {
		t.Fatalf("taken %d err %v rejections %v", taken, err, run.rejected)
	}
}

func TestSettingsCyclesAndSavesTheReopenChoices(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.settings.field = settingsFieldReopenSessions
	m.cycleSetting(1)
	m.settings.field = settingsFieldOutsidePanes
	m.cycleSetting(1)
	out := ansi.Strip(m.frame())
	for _, want := range []string{"on reopen", "always resume the ones that died", "outside panes", "always relaunch into the board"} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings missing %q:\n%s", want, out)
		}
	}
	m.saveAndCloseSettings()
	if got := m.reopenSessionsMode(); got != reopenResume {
		t.Fatalf("on reopen saved as %q", got)
	}
	if got := m.outsidePanesMode(); got != paneRelaunch {
		t.Fatalf("outside panes saved as %q", got)
	}
}

func TestAPaneAdoptedWhileRunningDoesNotPrompt(t *testing.T) {
	m := buildModel(t)
	m.adoptFirstDone = true
	m.noteAdopted(adoptedMsg{taken: 1, ids: []string{"later"}})
	if m.mode != modeList || m.errBar.text != "" {
		t.Fatalf("mode = %v notice = %q", m.mode, m.errBar.text)
	}
}

// A pane resumed by hand on a board session's conversation is a row of its
// own; the dead row it came from is a duplicate, never a loss. Offering it
// back, or reviving it, would put a second agent on one conversation.
func TestADeadRowWhoseConversationRunsElsewhereIsNeverOffered(t *testing.T) {
	dead := endRow("old")
	dead.AgentSessionID = "conv-1"
	live := store.Session{ID: "byhand", Name: "byhand", Tool: "claude", Status: status.Idle,
		AgentSessionID: "conv-1", TmuxSocket: "default", TmuxPaneID: "%3"}
	m := restoreModel(dead, live)
	if got := m.restoreCandidates(); len(got) != 0 {
		t.Fatalf("offered %v, want nothing", got)
	}
	if class := m.restore.ends["old"]; class.verdict != endSuperseded || !strings.Contains(class.why, "byhand") {
		t.Fatalf("verdict %+v", class)
	}
}

func TestReviveRefusesARowWhoseConversationIsRunningElsewhere(t *testing.T) {
	m := buildModel(t)
	lostSession(t, m, "old")
	if err := m.store.SetAgentSessionID("old", "conv-1"); err != nil {
		t.Fatal(err)
	}
	adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
	if err := m.store.SetAgentSessionID("byhand", "conv-1"); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)
	row, _ := m.store.Get("old")
	err := m.reviveSession(row)
	if err == nil || !strings.Contains(err.Error(), "already running in byhand") {
		t.Fatalf("revive error = %v", err)
	}
	if m.tmux.Exists("old") {
		t.Fatal("a second agent was started on the conversation")
	}
}

// The scan records the conversation a Claude Code pane is running, from the
// sidecar its process writes, so the rule above has something to match on.
func TestTheScanRecordsTheConversationAPaneIsRunning(t *testing.T) {
	m := buildModel(t)
	socket, _ := uiForeignServer(t, "cat")
	candidates := adopt.Panes(socket)
	if len(candidates) != 1 {
		t.Fatalf("candidates %v", candidates)
	}
	run := &adoptRun{
		tools: m.adoptTools(), known: map[string]bool{}, onBoard: map[string]bool{}, names: map[string]bool{},
		stor: m.store, driver: m.tmux, rejected: map[string]int{},
		claude: []convo.ClaudeSession{{PID: int(candidates[0].PID), SessionID: "conv-1"}},
	}
	if taken, err := run.take(candidates, adopt.NewProcTable()); err != nil || taken != 1 {
		t.Fatalf("taken %d err %v rejections %v", taken, err, run.rejected)
	}
	row, err := m.store.Get(run.takenIDs[0])
	if err != nil || row.AgentSessionID != "conv-1" {
		t.Fatalf("row %+v err %v, want the sidecar's conversation", row, err)
	}
}

// Lost sessions are asked about on the first pass even while the adopt scan
// is still running, so the card never lands late on an operator mid-task.
func TestTheCardDoesNotWaitForTheScanWhenSessionsWereLost(t *testing.T) {
	m := buildModel(t)
	m.restoreArmed = true
	lostSession(t, m, "lost")
	m.applyCmd(t, nil)
	if m.mode != modeRestorePrompt {
		t.Fatalf("the card should open on the first pass, mode = %v", m.mode)
	}
	pressKey(t, m, key("n"))
	m.noteAdopted(adoptedMsg{taken: 1, ids: []string{"late"}})
	if m.mode != modeList || m.errBar.text != "" {
		t.Fatalf("a late pane should not prompt, mode = %v notice = %q", m.mode, m.errBar.text)
	}
}

func TestOutsidePanesDoNotOpenAReopenCard(t *testing.T) {
	m := buildModel(t)
	m.restoreArmed = true
	adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
	m.applyCmd(t, nil)
	if m.mode != modeList || m.restoreAsked {
		t.Fatalf("the card opened before the scan answered, mode = %v", m.mode)
	}
	m.noteAdopted(adoptedMsg{})
	m.applyCmd(t, nil)
	if m.mode != modeList || !m.restoreAsked {
		t.Fatalf("after the scan no card should open, mode = %v", m.mode)
	}
	if _, err := m.store.Get("byhand"); err == nil {
		t.Fatal("the outside pane should be removed from the board")
	}
}
