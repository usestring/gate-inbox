package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

// wideWorkModel is one session on n open pull requests, the last of which
// needs a person.
func wideWorkModel(t *testing.T, n int) *Model {
	t.Helper()
	prs := map[string]forge.PR{}
	var prompt []string
	for i := 1; i <= n; i++ {
		pr := forge.PR{Repo: "example-org/sample-repo", Number: i, State: forge.PROpen, Mergeable: true,
			URL: "https://github.com/example-org/sample-repo/pull/" + strconv.Itoa(i)}
		if i == n {
			pr.Checks = forge.ChecksFailing
		}
		prs["pr:example-org/sample-repo#"+strconv.Itoa(i)] = pr
		prompt = append(prompt, pr.URL)
	}
	m := &Model{
		width: 100, height: 40,
		work: worktracker.New(fakeGit{}, fakePRs{prs: prs, health: forge.Health{OK: true}}, fakeTickets{health: forge.Health{OK: true}}),
		sessions: []store.Session{{ID: "s1", Name: "wide", Tool: "claude", Status: status.Idle, Cwd: "/repo",
			LaunchPrompt: strings.Join(prompt, "\n")}},
	}
	m.runWork(t)
	m.rebuildRows()
	return m
}

func railArtifacts(m *Model) []workRow {
	var rows []workRow
	for _, row := range m.rows {
		if row.isArtifact() {
			rows = append(rows, *row.art)
		}
	}
	return rows
}

// The rail hangs five rows at most, the one that needs a person among them,
// and a last row that says how many it left out.
func TestTheRailCapsASessionsWorkAndCountsTheRest(t *testing.T) {
	m := wideWorkModel(t, 12)
	rows := railArtifacts(m)
	if len(rows) != railWorkCap {
		t.Fatalf("rail hangs %d rows, want %d", len(rows), railWorkCap)
	}
	if !rows[0].needsYou {
		t.Errorf("the pull request that needs a person was cut: %+v", rows[0])
	}
	last := rows[len(rows)-1]
	if last.more != 8 || last.label != "8 more" {
		t.Errorf("the last row does not count the rest: %+v", last)
	}
	line := ansi.Strip(m.renderTreeRow(treeRow{sess: m.sessions[0], depth: 1, art: &last}, false, 80, 0, panelHex()))
	if !strings.Contains(line, "… 8 more") || !strings.Contains(line, "W shows all") {
		t.Errorf("the more row does not read as one: %q", line)
	}

	// Exactly the cap is drawn whole: a "1 more" row in place of a row is
	// no saving.
	if rows := railArtifacts(wideWorkModel(t, railWorkCap)); len(rows) != railWorkCap || rows[len(rows)-1].more != 0 {
		t.Errorf("a session at the cap grew a more row: %+v", rows)
	}
}

// W puts the rest back on the rail itself, and stays on the list: the rows
// the count stood for are hanging under the session it counted them for.
// A second W puts the cap back.
func TestWOnAWideSessionHangsAllOfItsWorkOnTheRail(t *testing.T) {
	m := wideWorkModel(t, 12)
	for i, row := range m.rows {
		if row.isSession() && row.sess.ID == "s1" {
			m.cursor = i
		}
	}
	m.handleKey(runeKey("W"))
	if m.mode != modeList {
		t.Fatalf("W left mode %v", m.mode)
	}
	rows := railArtifacts(m)
	if len(rows) != 12 {
		t.Fatalf("the rail hangs %d rows, want all twelve", len(rows))
	}
	for _, row := range rows {
		if row.more > 0 {
			t.Errorf("a count row survived the uncapped rail: %+v", row)
		}
	}

	m.handleKey(runeKey("W"))
	if rows := railArtifacts(m); len(rows) != railWorkCap {
		t.Fatalf("a second W left %d rows, want the cap back", len(rows))
	}
}

// A click can leave the cursor on the count line, and enter there is the
// same lift: the row stands for what the cap took off.
func TestEnterOnTheMoreRowShowsTheRowsItCounted(t *testing.T) {
	m := wideWorkModel(t, 12)
	for i, row := range m.rows {
		if row.isArtifact() && row.art.more > 0 {
			m.cursor = i
		}
	}
	m.handleKey(namedKey(tea.KeyEnter))
	if m.mode != modeList {
		t.Fatalf("enter on the more row left mode %v", m.mode)
	}
	if rows := railArtifacts(m); len(rows) != 12 {
		t.Fatalf("the rail hangs %d rows, want all twelve", len(rows))
	}
	// The cursor is off the row that has just gone, and on the session it
	// hung off.
	if entry, ok := m.cursorRow(); !ok || !entry.isSession() || entry.sess.ID != "s1" {
		t.Fatalf("the cursor was left on a row the lift removed: %+v", entry)
	}
}
