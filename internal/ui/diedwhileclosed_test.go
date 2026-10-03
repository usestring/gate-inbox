package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// restoreModel is a manager holding the given rows, with one tool that can
// resume by id, which is what separates an exact resume from a degraded one.
func restoreModel(sessions ...store.Session) *Model {
	return &Model{
		mode:         modeList,
		width:        120,
		height:       40,
		sessions:     sessions,
		restoreArmed: true,
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
	m.markDiedSessions()
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
	m.markDiedSessions()
	m.diedWhileClosed = nil
	m.errBar.text = ""
	m.markDiedSessions()
	if len(m.diedWhileClosed) != 0 || m.errBar.text != "" {
		t.Fatalf("the check ran twice: marks %v notice %q", m.diedWhileClosed, m.errBar.text)
	}
}

func TestStartupCheckStaysQuietWhenNothingIsDead(t *testing.T) {
	m := restoreModel(store.Session{ID: "a", Tool: "claude", Status: status.Working})
	m.markDiedSessions()
	if m.mode != modeList || m.errBar.text != "" {
		t.Fatalf("mode = %v notice = %q", m.mode, m.errBar.text)
	}
}

// A mark is about one lost agent run. Once the row is back, or has come back
// and been lost again, the startup check has said nothing about it.
func TestAMarkLapsesOnceTheRowIsRevived(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"))
	m.markDiedSessions()
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
	m.markDiedSessions()
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

func indexOf(sessions []store.Session, id string) int {
	for i, sess := range sessions {
		if sess.ID == id {
			return i
		}
	}
	return -1
}
