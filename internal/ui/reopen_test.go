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

// The scenario the startup check exists for: the board was closed, its own
// session was lost to a reboot, and meanwhile somebody started an agent by
// hand. Nothing is put over the list: the lost row is marked, the pane is
// taken over, and V brings the lost one back.
func TestReopenMarksLostSessionsAndTakesOutsidePanesOver(t *testing.T) {
	m := reopening(t)
	lostSession(t, m, "lost")
	socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)

	m.applyCmd(t, nil)
	if m.mode != modeList {
		t.Fatalf("mode = %v, want the list", m.mode)
	}
	row, _ := m.store.Get("lost")
	if !m.isDiedWhileClosed(row) {
		t.Fatal("the lost session was not marked")
	}
	if out := ansi.Strip(m.frame()); !strings.Contains(out, diedGlyph()) {
		t.Fatalf("the list does not show the mark:\n%s", out)
	}
	if foreignPaneAlive(t, socket, pane) {
		t.Fatal("the outside pane was not taken over")
	}
	if got, _ := m.store.Get("byhand"); got.TmuxPaneID != "" || !m.tmux.Exists("byhand") {
		t.Fatalf("the outside pane is not a board session now: %+v", got)
	}

	pressKey(t, m, key("V"))
	if !m.tmux.Exists("lost") {
		t.Fatal("V did not resume the lost session")
	}
}

// A session the operator ended is not marked, even across a reboot.
func TestReopenLeavesOutASessionTheOperatorKilled(t *testing.T) {
	m := reopening(t)
	lostSession(t, m, "killed")
	row, _ := m.store.Get("killed")
	if err := m.store.RecordEnd(row, store.EndKilled); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, nil)
	if row, _ = m.store.Get("killed"); m.isDiedWhileClosed(row) {
		t.Fatal("a killed session was marked")
	}
}

func TestEachReopenSessionsSettingValue(t *testing.T) {
	for _, stored := range []string{"", "ask", reopenMark} {
		t.Run("mark when "+stored, func(t *testing.T) {
			m := reopening(t)
			if stored != "" {
				setMode(t, m, reopenSessionsSetting, stored)
			}
			lostSession(t, m, "lost")
			m.applyCmd(t, nil)
			row, _ := m.store.Get("lost")
			if m.mode != modeList || !m.isDiedWhileClosed(row) || m.tmux.Exists("lost") {
				t.Fatalf("mark should flag the row and leave it, mode = %v", m.mode)
			}
		})
	}
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
		row, _ := m.store.Get("lost")
		if m.mode != modeList || m.tmux.Exists("lost") || m.isDiedWhileClosed(row) {
			t.Fatalf("never should neither mark nor resume, mode = %v", m.mode)
		}
		if !strings.Contains(m.errBar.text, "V revives") {
			t.Fatalf("notice = %q, want one line pointing at V", m.errBar.text)
		}
	})
}

func TestEachOutsidePanesSettingValue(t *testing.T) {
	for _, stored := range []string{"", "ask", paneRelaunch} {
		t.Run("take over when "+stored, func(t *testing.T) {
			m := reopening(t)
			if stored != "" {
				setMode(t, m, outsidePanesSetting, stored)
			}
			socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
			m.applyCmd(t, nil)
			if m.mode != modeList {
				t.Fatalf("panes are never asked about, mode = %v", m.mode)
			}
			if foreignPaneAlive(t, socket, pane) || !m.tmux.Exists("byhand") {
				t.Fatal("the idle pane should be taken over into the board")
			}
		})
	}
	t.Run("adopt", func(t *testing.T) {
		m := reopening(t)
		setMode(t, m, outsidePanesSetting, paneAdopt)
		socket, pane := adoptForeignPane(t, m, "byhand", "byhand", status.Idle)
		m.applyCmd(t, nil)
		if m.mode != modeList {
			t.Fatalf("adopt should not ask, mode = %v", m.mode)
		}
		if got, _ := m.store.Get("byhand"); got.TmuxPaneID == "" || !foreignPaneAlive(t, socket, pane) {
			t.Fatal("adopt should leave the pane as it is")
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

// A pane left off the board is remembered by the scan, which does not take
// it again while it runs.
func TestTheScanRemembersALeftOutPane(t *testing.T) {
	m := buildModel(t)
	socket, _ := adoptForeignPane(t, m, "left", "left", status.Idle)
	m.applyCmd(t, nil)
	if n := m.leaveOutPanes(m.adoptedCandidates()); n != 1 {
		t.Fatalf("left out %d panes, want 1", n)
	}
	if _, err := m.store.Get("left"); err == nil {
		t.Fatal("left should be off the board")
	}

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
	taken, err := run.take(adopt.Panes(socket), adopt.NewProcTable())
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
	m.cycleSetting(-1)
	out := ansi.Strip(m.frame())
	for _, want := range []string{"on reopen", "always resume the ones that died", "outside panes", "ignore them"} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings missing %q:\n%s", want, out)
		}
	}
	m.saveAndCloseSettings()
	if got := m.reopenSessionsMode(); got != reopenResume {
		t.Fatalf("on reopen saved as %q", got)
	}
	if got := m.outsidePanesMode(); got != paneIgnore {
		t.Fatalf("outside panes saved as %q", got)
	}
}

// A pane started while the board is up does not interrupt whatever the
// operator is doing: one line says it will be taken over, or, with panes
// kept as they are, points at O.
func TestAPaneAdoptedWhileRunningGetsOneLine(t *testing.T) {
	m := buildModel(t)
	m.restoreArmed = true
	m.adoptFirstDone = true
	m.noteAdopted(adoptedMsg{taken: 1, ids: []string{"later"}})
	if m.mode != modeList || !strings.Contains(m.errBar.text, "taking it over once idle") {
		t.Fatalf("mode = %v notice = %q", m.mode, m.errBar.text)
	}
	setMode(t, m, outsidePanesSetting, paneAdopt)
	m.noteAdopted(adoptedMsg{taken: 1, ids: []string{"later"}})
	if m.mode != modeList || !strings.Contains(m.errBar.text, "O takes it over") {
		t.Fatalf("mode = %v notice = %q", m.mode, m.errBar.text)
	}
}

// A pane resumed by hand on a board session's conversation is a row of its
// own; the dead row it came from is a duplicate, never a loss. Offering it
// back, or reviving it, would put a second agent on one conversation.
func TestADeadRowWhoseConversationRunsElsewhereIsNeverMarked(t *testing.T) {
	dead := endRow("old")
	dead.AgentSessionID = "conv-1"
	live := store.Session{ID: "byhand", Name: "byhand", Tool: "claude", Status: status.Idle,
		AgentSessionID: "conv-1", TmuxSocket: "default", TmuxPaneID: "%3"}
	m := restoreModel(dead, live)
	got, ends := m.classifyDeadRows(endEvidence{})
	if len(got) != 0 {
		t.Fatalf("offered %v, want nothing", got)
	}
	if class := ends["old"]; class.verdict != endSuperseded || !strings.Contains(class.why, "byhand") {
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
