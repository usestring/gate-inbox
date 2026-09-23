// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
)

// rail is the sidebar alone. The content column no longer carries a detail
// head of its own, but the rail's own opening block repeats the selected
// row's name below the list, so asserting against a whole frame would still
// pass on a row the list itself never painted.
func (m *Model) rail() string {
	return ansi.Strip(railLinesText(m.railLines(40, m.listBodyHeight())))
}

// railRow finds the one entry-list line naming a session, or -2 when two do.
// The search stops at the first rule, since the opening block below the
// list repeats the selected row's own name in its own identity line.
//
// The match ends at a name boundary rather than anywhere in the line: shells
// are named after the agent they hang under, so "terminal-agent-one" is a
// prefix of "terminal-agent-one-2" and a plain substring test reports the
// unambiguous row as ambiguous.
func railRow(m *Model, needle string) int {
	at := -1
	for i, row := range m.railLines(40, m.listBodyHeight()) {
		if row.rule {
			break
		}
		if !namesRow(ansi.Strip(row.text), needle) {
			continue
		}
		if at >= 0 {
			return -2
		}
		at = i
	}
	return at
}

// namesRow reports whether a rail line names exactly this session, and not one
// whose name merely starts with it.
func namesRow(line, needle string) bool {
	for at := 0; ; {
		i := strings.Index(line[at:], needle)
		if i < 0 {
			return false
		}
		end := at + i + len(needle)
		if end >= len(line) || !isNameByte(line[end]) {
			return true
		}
		at = end
	}
}

func isNameByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return b == '-' || b == '_'
}

func groupWithShell(t *testing.T, m *Model, group string) {
	t.Helper()
	if err := m.store.CreateGroup(group, t.TempDir()); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, group)
}

func TestRestingShellUsesCaretGlyph(t *testing.T) {
	m := buildModel(t)
	m.applyCmd(t, m.refreshCmd())
	shell := spawnTerminal(t, m)
	for i := range m.rows {
		if m.rows[i].sess.ID == shell.ID {
			m.rows[i].sess.Status = status.Idle
		}
	}

	if rail := m.rail(); !strings.Contains(rail, shellGlyph) {
		t.Fatalf("a resting shell should carry the caret:\n%s", rail)
	}
}

func TestDeadInlineShellKeepsItsStatusGlyph(t *testing.T) {
	m := buildModel(t)
	m.applyCmd(t, m.refreshCmd())
	shell := spawnTerminal(t, m)
	shell.Status = status.Idle

	if glyph := ansi.Strip(m.sessionGlyph(shell)); glyph != shellGlyph {
		t.Fatalf("a resting shell wears the caret, got %q", glyph)
	}
	for _, gone := range []string{status.Dead, status.Errored} {
		shell.Status = gone
		glyph := ansi.Strip(m.sessionGlyph(shell))
		if glyph == shellGlyph {
			t.Fatalf("a %s shell must not read as resting", gone)
		}
		if glyph != statusGlyph(gone) {
			t.Fatalf("a %s shell should wear its own glyph, got %q", gone, glyph)
		}
	}
}

func TestUnnestedShellSitsInItsGroup(t *testing.T) {
	m := buildModel(t)
	groupWithShell(t, m, "backend")
	shell := spawnTerminal(t, m)

	var row treeRow
	for _, entry := range m.rows {
		if !entry.isGroup && entry.sess.ID == shell.ID {
			row = entry
		}
	}
	if row.depth != 1 {
		t.Fatalf("unnested shell depth = %d, want 1 (under its group)", row.depth)
	}
	if rail := m.rail(); strings.Contains(rail, "Terminals") {
		t.Fatalf("the list paints no divider:\n%s", rail)
	}
}

func TestRollupsCountAgentsOnly(t *testing.T) {
	m := buildModel(t)
	groupWithShell(t, m, "backend")
	createSession(t, m, "agent-one", t.TempDir(), "backend")
	m.selectGroupRow(t, "backend")
	shell := spawnTerminal(t, m)

	if got := m.groupSessionCount("backend"); got != 1 {
		t.Fatalf("groupSessionCount = %d, want 1 agent", got)
	}
	total := 0
	for _, count := range m.groupStatusCounts("backend") {
		total += count
	}
	if total != 1 {
		t.Fatalf("group status counts total = %d, want 1", total)
	}
	if got := len(m.listedAgents()); got != 1 {
		t.Fatalf("listedAgents = %d, want it to count the agent alone", got)
	}
	roster := ansi.Strip(m.viewGroupAgents("backend", 112, 10))
	if !strings.Contains(roster, "agent-one") || strings.Contains(roster, shell.Name) {
		t.Fatalf("the roster should hold the agent and not the shell:\n%s", roster)
	}
}

func TestCursorOnShellStaysPainted(t *testing.T) {
	m := buildModel(t)
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "agent-one", t.TempDir(), "")
	first := spawnTerminal(t, m)
	second := spawnTerminal(t, m)
	if first.ID == second.ID {
		t.Fatal("the two spawns should be different shells")
	}

	for _, shell := range []string{first.Name, second.Name} {
		m.selectSessionRow(t, shell)
		sess, ok := m.selected()
		if !ok || sess.Name != shell {
			t.Fatalf("selected() = %+v %v, want %s", sess, ok, shell)
		}
		for _, height := range []int{16, 24, 30, 44} {
			m.height = height
			if rail := m.rail(); railRow(m, shell) < 0 {
				t.Fatalf("selected shell %s is unpainted at height %d:\n%s", shell, height, rail)
			}
		}
	}
}

func TestRailReturnsItsBudget(t *testing.T) {
	m := buildModel(t)
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "agent-one", t.TempDir(), "")
	spawnTerminal(t, m)
	spawnTerminal(t, m)

	for _, height := range []int{3, 4, 6, 8, 10, 14, 34} {
		for _, width := range []int{30, 60, 120} {
			for _, searching := range []bool{false, true} {
				m.searching = searching
				if got := len(m.railLines(width, height)); got != height {
					t.Fatalf("%dx%d searching=%v returned %d rows, want %d", width, height, searching, got, height)
				}
			}
		}
	}
}
