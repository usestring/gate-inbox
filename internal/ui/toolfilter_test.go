package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func toolFilterFixture(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	for _, sess := range []store.Session{
		{ID: "c1", Name: "claude-one", Tool: "claude", Cwd: "/tmp", Status: status.Waiting},
		{ID: "c2", Name: "claude-two", Tool: "claude", Cwd: "/tmp", Status: status.Idle},
		{ID: "x1", Name: "codex-one", Tool: "codex", Cwd: "/tmp", Status: status.Waiting},
		{ID: "o1", Name: "opencode-one", Tool: "opencode", Cwd: "/tmp", Status: status.Finished},
	} {
		if err := m.store.CreateSession(sess); err != nil {
			t.Fatalf("create session %q: %v", sess.ID, err)
		}
	}
	loadStoredRows(t, m)
	return m
}

func TestToolFilterCyclesThroughHarnesses(t *testing.T) {
	m := toolFilterFixture(t)
	seen := map[string]bool{}
	for range 5 {
		updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
		m = updated.(*Model)
		if cmd != nil {
			m.applyCmd(t, cmd)
		}
		seen[m.toolFilter] = true
	}
	for _, want := range []string{"", "claude", "codex", "opencode"} {
		if !seen[want] {
			t.Fatalf("cycle never reached %q, visited %v", want, seen)
		}
	}
}

func TestToolFilterNarrowsTheList(t *testing.T) {
	m := toolFilterFixture(t)
	m.toolFilter = "claude"
	m.rebuildRows()
	if got := sessionNames(m); !slices.Equal(got, []string{"claude-one", "claude-two"}) {
		t.Fatalf("filtered list = %v want the two claude sessions", got)
	}
	m.width, m.height = 120, 34
	rail := ansi.Strip(railLinesText(m.railLines(36, m.listBodyHeight())))
	if !strings.Contains(rail, "CLAUDE") {
		t.Fatalf("rail missing CLAUDE badge:\n%s", rail)
	}
	if !strings.Contains(rail, "show all") {
		t.Fatalf("rail badge should offer clearing the filter:\n%s", rail)
	}
}

func TestToolFilterEmptyState(t *testing.T) {
	m := shotModel()
	m.sessions = []store.Session{
		{ID: "idle", Name: "quiet", Tool: "claude", Cwd: "/tmp", Status: status.Idle},
	}
	m.toolFilter = "ghost-harness"
	m.rebuildRows()
	rail := ansi.Strip(strings.Join(splitLines(joinContentText(m.railLines(40, 20))), "\n"))
	if !strings.Contains(rail, "nothing on ghost-harness") {
		t.Fatalf("rail missing empty tool copy:\n%s", rail)
	}
}

func TestToolFilterNarrowsTriage(t *testing.T) {
	m := toolFilterFixture(t)
	m.toolFilter = "codex"
	m.rebuildRows()
	m.selectSessionRow(t, "codex-one")

	updated, cmd := m.handleKey(key("i"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !m.triage {
		t.Fatal("i did not turn triage on")
	}
	if got := sessionNames(m); !slices.Equal(got, []string{"codex-one"}) {
		t.Fatalf("triage queue = %v want only the codex session", got)
	}
	if rail := triageRail(m); !strings.Contains(rail, "CODEX") {
		t.Fatalf("triage rail lost the tool badge:\n%s", rail)
	}
}

func TestToolFilterCombinesWithAttention(t *testing.T) {
	m := toolFilterFixture(t)
	m.toolFilter = "claude"
	m.statusFilter = statusFilterAttention
	m.rebuildRows()
	if got := sessionNames(m); !slices.Equal(got, []string{"claude-one"}) {
		t.Fatalf("combined list = %v want only waiting claude", got)
	}
}
