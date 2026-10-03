package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// restoreModel is a manager holding the given rows, with one tool that can
// resume by id, which is what separates an exact resume from a degraded one.
func restoreModel(sessions ...store.Session) *Model {
	return &Model{
		mode:           modeList,
		width:          120,
		height:         40,
		sessions:       sessions,
		restoreArmed:   true,
		adoptFirstDone: true,
		cfg: config.Config{Tools: map[string]config.Tool{
			"claude": {ResumeByIDCommand: "claude --resume {id}"},
		}},
	}
}

func deadSession(id, name, agentID string) store.Session {
	return store.Session{ID: id, Name: name, Tool: "claude", Cwd: "/repo",
		Status: status.Dead, AgentSessionID: agentID,
		AgentLaunchedAt: launchedAt, LastStatusAt: launchedAt.Add(time.Hour)}
}

// launchedAt stands for the start of the agent that was lost. A mark keys on
// it, so a revived row stops wearing one.
var launchedAt = time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)

// The launch is never held: the startup check marks rows and stays on the
// list.
func TestStartupMarksDeadRowsAndStaysOnTheList(t *testing.T) {
	m := restoreModel(
		deadSession("a", "alpha", "id-a"),
		store.Session{ID: "b", Name: "beta", Tool: "claude", Status: status.Working},
		store.Session{ID: "c", Name: "gamma", Tool: "claude", Status: status.Dead, Archived: true},
	)
	m.markDiedSessions(time.Now())
	if m.mode != modeList {
		t.Fatalf("mode = %v, want the list", m.mode)
	}
	for id, want := range map[string]bool{"a": true, "b": false, "c": false} {
		if got := m.isDiedWhileClosed(m.sessions[indexOf(m.sessions, id)]); got != want {
			t.Errorf("%s marked = %v, want %v", id, got, want)
		}
	}
	if !strings.Contains(m.errBar.text, "1 session stopped without you ending them") ||
		!strings.Contains(m.errBar.text, "V revives") {
		t.Fatalf("notice = %q", m.errBar.text)
	}
}

func TestStartupCheckRunsOnce(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"))
	m.markDiedSessions(time.Now())
	m.diedWhileClosed = nil
	m.errBar.text = ""
	m.markDiedSessions(time.Now())
	if len(m.diedWhileClosed) != 0 || m.errBar.text != "" {
		t.Fatalf("the check ran twice: marks %v notice %q", m.diedWhileClosed, m.errBar.text)
	}
}

func TestStartupCheckStaysQuietWhenNothingIsDead(t *testing.T) {
	m := restoreModel(store.Session{ID: "a", Tool: "claude", Status: status.Working})
	m.markDiedSessions(time.Now())
	if m.mode != modeList || m.errBar.text != "" {
		t.Fatalf("mode = %v notice = %q", m.mode, m.errBar.text)
	}
}

// A mark is about one lost agent run. Once the row is back, or has come back
// and been lost again, the startup check has said nothing about it.
func TestAMarkLapsesOnceTheRowIsRevived(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"))
	m.markDiedSessions(time.Now())
	if !m.isDiedWhileClosed(m.sessions[0]) {
		t.Fatal("the lost row was not marked")
	}
	running := m.sessions[0]
	running.Status = status.Idle
	if m.isDiedWhileClosed(running) {
		t.Error("a running row still wears the mark")
	}
	lostAgain := m.sessions[0]
	lostAgain.AgentLaunchedAt = launchedAt.Add(time.Hour)
	if m.isDiedWhileClosed(lostAgain) {
		t.Error("a row lost again on a later launch still wears the first mark")
	}
}

func TestAttentionFilterKeepsADiedRow(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"), deadSession("b", "beta", "id-b"))
	m.markDiedSessions(time.Now())
	delete(m.diedWhileClosed, "b")
	m.statusFilter = statusFilterAttention
	if !m.attentionViaChild(m.sessions[0]) {
		t.Error("the attention filter dropped a row that died while the board was closed")
	}
	if m.attentionViaChild(m.sessions[1]) {
		t.Error("the attention filter kept an unmarked dead row")
	}
}

func TestAttentionFilterKeepsParentOfDiedChild(t *testing.T) {
	for _, state := range []string{"marked", "unmarked", "archived", "revived"} {
		t.Run(state, func(t *testing.T) {
			m := buildModel(t)
			child := deadSession("child", "child", "id-child")
			child.ParentID = "parent"
			m.markDied([]store.Session{child})
			switch state {
			case "unmarked":
				delete(m.diedWhileClosed, child.ID)
			case "archived":
				child.Archived = true
			case "revived":
				child.Status = status.Working
			}
			m.sessions = []store.Session{
				{ID: "parent", Name: "parent", Tool: "claude", Status: status.Working},
				child,
			}
			m.statusFilter = statusFilterAttention
			m.rebuildRows()
			if state != "marked" {
				if got := sessionNames(m); len(got) != 0 {
					t.Fatalf("attention list = %v, want no sessions", got)
				}
				return
			}
			if got := sessionNames(m); len(got) != 2 || got[0] != "parent" || got[1] != "child" {
				t.Fatalf("attention list = %v, want parent followed by child", got)
			}
			parentDepth := -1
			for _, row := range m.rows {
				if row.sess.ID == "parent" {
					parentDepth = row.depth
				}
				if row.sess.ID == "child" && row.depth != parentDepth+1 {
					t.Fatalf("child depth = %d, parent depth = %d, want child nested under parent", row.depth, parentDepth)
				}
			}
		})
	}
}

func TestAttentionFilterKeepsAncestorsOfDiedGrandchild(t *testing.T) {
	for _, state := range []string{"marked", "unmarked", "archived", "revived"} {
		t.Run(state, func(t *testing.T) {
			m := buildModel(t)
			terminal := deadSession("terminal", "terminal", "id-terminal")
			terminal.Tool = "shell"
			terminal.ParentID = "child"
			m.markDied([]store.Session{terminal})
			switch state {
			case "unmarked":
				delete(m.diedWhileClosed, terminal.ID)
			case "archived":
				terminal.Archived = true
			case "revived":
				terminal.Status = status.Working
			}
			m.sessions = []store.Session{
				{ID: "parent", Name: "parent", Tool: "claude", Status: status.Working},
				{ID: "child", Name: "child", ParentID: "parent", Tool: "claude", Status: status.Working},
				terminal,
			}
			m.statusFilter = statusFilterAttention
			m.rebuildRows()
			if state != "marked" {
				if got := sessionNames(m); len(got) != 0 {
					t.Fatalf("attention list = %v, want no sessions", got)
				}
				return
			}
			if got := sessionNames(m); !slices.Equal(got, []string{"parent", "child", "terminal"}) {
				t.Fatalf("attention list = %v, want parent, child, and terminal", got)
			}
			depths := make(map[string]int)
			for _, row := range m.rows {
				depths[row.sess.ID] = row.depth
			}
			if depths["child"] != depths["parent"]+1 || depths["terminal"] != depths["child"]+1 {
				t.Fatalf("attention tree depths = %v, want the full parent chain", depths)
			}
		})
	}
}

func indexOf(sessions []store.Session, id string) int {
	for i, sess := range sessions {
		if sess.ID == id {
			return i
		}
	}
	return -1
}

func TestStartupCheckWaitsForAdoptionAndFreshPoll(t *testing.T) {
	for _, mode := range []string{reopenMark, reopenResume} {
		t.Run(mode, func(t *testing.T) {
			m := reopening(t)
			setMode(t, m, reopenSessionsSetting, mode)
			m.sessions = []store.Session{deadSession("old", "old", "conversation")}
			m.adoptFirstDone = false
			listedAt := time.Now()
			assertPending := func() {
				t.Helper()
				m.markDiedSessions(listedAt)
				if m.restoreChecked || len(m.diedWhileClosed) != 0 || len(m.launched) != 0 {
					t.Fatal("startup classified or resumed a row before adoption was visible")
				}
			}
			assertPending()
			m.noteAdopted(adoptedMsg{taken: 2, ids: []string{"live", "other"}})
			assertPending()
			m.sessions = append(m.sessions, store.Session{ID: "live", Tool: "claude",
				AgentSessionID: "conversation", Status: status.Working})
			assertPending()
			m.sessions = append(m.sessions, store.Session{ID: "other", Tool: "claude", Status: status.Working})
			m.markDiedSessions(time.Now())
			if !m.restoreChecked || len(m.diedWhileClosed) != 0 || len(m.launched) != 0 {
				t.Fatal("startup did not exclude the conversation already running in an adopted pane")
			}
		})
	}
}

func TestStartupCheckContinuesAfterAdoptionFindsNoRows(t *testing.T) {
	m := restoreModel(deadSession("lost", "lost", "conversation"))
	m.adoptFirstDone = false
	m.markDiedSessions(time.Now())
	if m.restoreChecked {
		t.Fatal("startup check ran before adoption finished")
	}
	m.noteAdopted(adoptedMsg{})
	m.markDiedSessions(time.Now())
	if !m.restoreChecked || !m.isDiedWhileClosed(m.sessions[0]) {
		t.Fatal("startup did not mark a lost row after the empty adoption scan")
	}
}

func TestStartupCheckRejectsPollStartedBeforeAdoption(t *testing.T) {
	for _, mode := range []string{reopenMark, reopenResume} {
		t.Run(mode, func(t *testing.T) {
			m := reopening(t)
			setMode(t, m, reopenSessionsSetting, mode)
			stalePoll := time.Now()
			m.sessions = []store.Session{
				deadSession("old", "old", "conversation"),
				deadSession("adopted", "adopted", "conversation"),
			}
			m.noteAdopted(adoptedMsg{taken: 1, ids: []string{"adopted"}})
			for _, listedAt := range []time.Time{stalePoll, m.adoptFinishedAt} {
				m.markDiedSessions(listedAt)
				if m.restoreChecked || len(m.diedWhileClosed) != 0 || len(m.launched) != 0 {
					t.Fatal("startup classified a poll that had not started after adoption")
				}
			}
			m.sessions[1].Status = status.Working
			m.markDiedSessions(m.adoptFinishedAt.Add(time.Nanosecond))
			if !m.restoreChecked || len(m.diedWhileClosed) != 0 || len(m.launched) != 0 {
				t.Fatal("post-adoption poll did not exclude the conversation already running")
			}
		})
	}
}

func TestStartupCheckContinuesAfterAdoptedRowIsPruned(t *testing.T) {
	m := reopening(t)
	lost := deadSession("lost", "lost", "conversation")
	adopted := deadSession("outside", "outside", "other-conversation")
	adopted.TmuxPaneID = "%1"
	if err := m.store.CreateSession(adopted); err != nil {
		t.Fatal(err)
	}
	listedAt := time.Now()
	m.noteAdopted(adoptedMsg{taken: 1, ids: []string{adopted.ID}})
	scan := tmux.PaneScan{Gone: map[string]bool{adopted.ID: true}}
	var err error
	m.sessions, err = m.poller.pruneGoneAdopted([]store.Session{lost, adopted}, scan)
	if err != nil {
		t.Fatal(err)
	}
	m.markDiedSessions(listedAt)
	if m.restoreChecked {
		t.Fatal("startup classified the stale first missing pass")
	}
	m.sessions, err = m.poller.pruneGoneAdopted(m.sessions, scan)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.sessions) != 1 || m.sessions[0].ID != lost.ID {
		t.Fatalf("sessions after pruning = %v, want only lost", m.sessions)
	}
	m.markDiedSessions(m.adoptFinishedAt.Add(time.Nanosecond))
	if !m.restoreChecked || !m.isDiedWhileClosed(lost) {
		t.Fatal("pruned adopted row prevented startup from marking an unrelated lost row")
	}
}
