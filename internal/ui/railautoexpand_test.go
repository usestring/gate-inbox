package ui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

// undecidedFleet is the fleet with nothing said about any session's work,
// which is how a rail arrives before anybody folds anything. The cursor is
// parked on root, a group row, so no session is open either.
func undecidedFleet(tb testing.TB, n, width, height int) *Model {
	tb.Helper()
	m := fleetModel(tb, n, width, height)
	for _, sess := range m.sessions {
		m.clearWorkFold(sess.ID)
	}
	m.cursor = 0
	m.rebuildRows()
	return m
}

// sessionRowIndex is where a session sits in the tree right now.
func sessionRowIndex(tb testing.TB, m *Model, id string) int {
	tb.Helper()
	for i, row := range m.rows {
		if row.isSession() && row.sess.ID == id {
			return i
		}
	}
	tb.Fatalf("no row for session %q", id)
	return -1
}

// firstWorkingSession is the first session in the tree that is on something
// and named short enough that its row still has room for the counts. The
// fixture alternates a short name with one built to be truncated, and a
// truncated name is a row with no meta line left to read.
func firstWorkingSession(tb testing.TB, m *Model) store.Session {
	tb.Helper()
	for _, row := range m.rows {
		if row.isSession() && m.hasRailWork(row.sess) && len(row.sess.Name) <= 12 {
			return row.sess
		}
	}
	tb.Fatal("no session in the fixture is on anything")
	return store.Session{}
}

// fleetIndex recovers which of the fixture's sessions this is, which is what
// says the pull request and the ticket it should be showing.
func fleetIndex(tb testing.TB, sess store.Session) int {
	tb.Helper()
	i, err := strconv.Atoi(strings.TrimPrefix(sess.ID, "sess-"))
	if err != nil {
		tb.Fatalf("session id %q is not the fixture's", sess.ID)
	}
	return i
}

// artifactOwners is which sessions currently have their work on screen.
func artifactOwners(m *Model) map[string]int {
	owners := map[string]int{}
	for _, row := range m.rows {
		if row.isArtifact() {
			owners[row.sess.ID]++
		}
	}
	return owners
}

// The whole point of the change: the row you are on shows what it is working
// on, and no other row does. Eighty-odd sessions carrying seventy-odd
// references cannot all be open at once, and one at a time is the only count
// that keeps the fleet on one screen.
func TestOnlyTheCursorSessionOpensItsWork(t *testing.T) {
	// Taller than the other fixtures in this file: the rail's own detail
	// block for the cursor session now sits above the dock too, and the
	// folded-row assertion below needs the list to keep more of the fleet
	// in view around the cursor than a 40-row terminal leaves it.
	m := undecidedFleet(t, fleetSize, 120, 56)
	if owners := artifactOwners(m); len(owners) != 0 {
		t.Fatalf("a tree with the cursor on root opened work anyway: %v", owners)
	}

	sess := firstWorkingSession(t, m)
	m.cursor = sessionRowIndex(t, m, sess.ID)
	m.rebuildRows()

	owners := artifactOwners(m)
	if len(owners) != 1 || owners[sess.ID] == 0 {
		t.Fatalf("open work belongs to %v, want only %q", owners, sess.ID)
	}
	rail := ansi.Strip(railLinesText(m.railLines(80, m.listBodyHeight())))
	t.Logf("cursor on %s:\n%s", sess.Name, rail)
	index := fleetIndex(t, sess)
	for _, want := range []string{"#" + strconv.Itoa(fleetPR(index)), fleetTicket(index)} {
		if !strings.Contains(rail, want) {
			t.Fatalf("the cursor row does not list %q:\n%s", want, rail)
		}
	}
	// Every other session on something is a line of counts instead.
	if !strings.Contains(rail, "1 pr · 1 issue") {
		t.Fatalf("no folded row counted its work:\n%s", rail)
	}
}

// Moving off a session takes its work with it, and one step is one session
// however many references the row that is leaving had open.
func TestSteppingOffASessionClosesItsWork(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 120, 40)
	sess := firstWorkingSession(t, m)
	m.cursor = sessionRowIndex(t, m, sess.ID)
	m.rebuildRows()
	if artifactOwners(m)[sess.ID] == 0 {
		t.Fatal("the cursor session did not open")
	}

	m.moveCursor(1)
	if owners := artifactOwners(m); owners[sess.ID] != 0 {
		t.Fatalf("%q kept its work open after the cursor left: %v", sess.ID, owners)
	}
	if row, ok := m.cursorRow(); !ok || row.isArtifact() {
		t.Fatalf("one step down landed on %+v", row)
	}
}

// Holding ↓ crosses the fleet. Every session is reached, in the order the
// tree lists them, exactly once, and no step ever lands on a pull request.
func TestHoldingDownVisitsEverySessionExactlyOnce(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 120, 40)

	var want []string
	for _, row := range m.rows {
		if row.isSession() {
			want = append(want, row.sess.ID)
		}
	}
	steps := len(m.rows)
	if len(want) < 2 {
		t.Fatalf("fixture lists %d sessions", len(want))
	}

	var got []string
	groups := 0
	for step := 0; step < steps; step++ {
		m.moveCursor(1)
		row, ok := m.cursorRow()
		if !ok {
			t.Fatalf("step %d left the cursor off the tree", step)
		}
		if row.isArtifact() {
			t.Fatalf("step %d landed on %s, which is work and not a session", step, row.art.label)
		}
		if row.isGroup {
			groups++
			continue
		}
		got = append(got, row.sess.ID)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the walk visited\n%v\nwant\n%v", got, want)
	}
	if groups == 0 {
		t.Fatal("the walk skipped every group row as well")
	}
	if m.cursor != 0 {
		t.Fatalf("a full lap ended on row %d, want back at the top", m.cursor)
	}
}

// Down then up has to come back to where it started, at the top of the list,
// at the bottom, and across a group boundary. Auto-expansion inserts rows
// below the cursor and removes rows above it, which is exactly the shape that
// makes a step overshoot.
func TestSteppingIsReversible(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 120, 40)
	for _, start := range []int{0, 1, len(m.rows) / 2, len(m.rows) - 1} {
		m.cursor = start
		m.rebuildRows()
		startKey := rowKey(m.rows[m.cursor])

		var down []string
		for i := 0; i < 12; i++ {
			m.moveCursor(1)
			down = append(down, rowKey(m.rows[m.cursor]))
		}
		for i := 0; i < 12; i++ {
			m.moveCursor(-1)
		}
		if got := rowKey(m.rows[m.cursor]); got != startKey {
			t.Fatalf("from %q, twelve steps down and back landed on %q\n%v", startKey, got, down)
		}
		for i := range down {
			if i > 0 && down[i] == down[i-1] {
				t.Fatalf("from %q the walk stalled on %q", startKey, down[i])
			}
		}
	}
}

// An explicit fold is a decision and a default is not, so ← on the row the
// cursor is on shuts it and keeps it shut.
func TestAnExplicitFoldBeatsAutoExpansion(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 120, 40)
	sess := firstWorkingSession(t, m)
	m.cursor = sessionRowIndex(t, m, sess.ID)
	m.rebuildRows()

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	*m = *updated.(*Model)
	if owners := artifactOwners(m); owners[sess.ID] != 0 {
		t.Fatalf("← left %q open: %v", sess.ID, owners)
	}
	m.rebuildRows()
	if owners := artifactOwners(m); owners[sess.ID] != 0 {
		t.Fatalf("the next poll reopened a session the user shut: %v", owners)
	}
	// And the badge comes back with it, because the row is folded again.
	rail := ansi.Strip(railLinesText(m.railLines(80, m.listBodyHeight())))
	if !strings.Contains(railRowLine(t, rail, sess.Name), "1 pr · 1 issue") {
		t.Fatalf("a row the user folded lost its counts:\n%s", rail)
	}
}

// The other half of the same rule: a session the user opened stays open once
// the cursor has moved on.
func TestAnExplicitUnfoldSurvivesTheCursorLeaving(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 120, 40)
	sess := firstWorkingSession(t, m)
	m.setWorkFolded(sess.ID, false)
	m.rebuildRows()

	m.cursor = 0
	m.rebuildRows()
	if owners := artifactOwners(m); owners[sess.ID] == 0 {
		t.Fatalf("a session the user opened shut itself when the cursor left: %v", owners)
	}
}

// F folds the tree, and has to leave the rail somewhere auto-expansion can
// still work from: writing "folded" on every session would decide the whole
// fleet at once and the cursor would never open anything again.
func TestFoldAllLeavesTheRailUndecided(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 200, 50)
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	m.store = st
	sess := firstWorkingSession(t, m)
	m.setWorkFolded(sess.ID, false)
	m.rebuildRows()

	m.toggleCollapseAll()
	for _, s := range m.sessions {
		if _, decided := m.workFoldDecision(s.ID); decided {
			t.Fatalf("fold-all decided %q instead of forgetting it", s.ID)
		}
	}
	if !m.allFoldsCollapsed() {
		t.Fatal("fold-all did not leave the tree folded, so F cannot reverse")
	}

	m.toggleCollapseAll()
	if m.allFoldsCollapsed() {
		t.Fatal("a second F did not unfold the tree")
	}
	for _, s := range m.sessions {
		if !m.hasRailWork(s) {
			continue
		}
		if !m.railWorkExpanded(s.ID) {
			t.Fatalf("unfold-all left %q shut", s.ID)
		}
	}
}

// countModel is one session on ten pull requests and three tickets, which is
// what the counts are for: a rail that named two of thirteen was the shape
// this badge replaced.
func countModel(t *testing.T, prs, tickets int) *Model {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	refs := make([]string, 0, prs+tickets)
	forgePRs := map[string]forge.PR{}
	for i := 0; i < prs; i++ {
		number := 800 + i
		refs = append(refs, fmt.Sprintf("PR #%d", number))
		forgePRs["pr:example-org/sample-repo#"+strconv.Itoa(number)] = forge.PR{
			Repo: "example-org/sample-repo", Number: number, State: forge.PROpen,
			Checks: forge.ChecksPassing, Mergeable: true,
		}
	}
	forgeTickets := map[string]forge.Ticket{}
	for i := 0; i < tickets; i++ {
		id := fmt.Sprintf("ABC-1004%02d", i)
		refs = append(refs, id)
		forgeTickets["ticket:"+id] = forge.Ticket{
			Identifier: id, State: "In Review", StateType: "started",
		}
	}
	now := time.Now()
	m := &Model{
		width: 120, height: 34, mode: modeList, store: st,
		collapsed: map[string]bool{}, split: splitState{ratio: defaultSplitRatio},
		sessions: []store.Session{{
			ID: "busy", Name: "busy", Tool: "claude", Status: status.Working,
			Cwd: "/Users/someone/dev/api", LaunchPrompt: strings.Join(refs, " "),
			CreatedAt: now.Add(-24 * time.Hour), LastStatusAt: now.Add(-time.Minute),
		}},
		work: worktracker.New(
			fakeGit{remote: "git@github.com:example-org/sample-repo.git"},
			fakePRs{prs: forgePRs, health: forge.Health{OK: true}},
			fakeTickets{tickets: forgeTickets, health: forge.Health{OK: true}},
		),
	}
	m.runWork(t)
	m.rebuildRows()
	return m
}

// The badge gives ground in a fixed order: the punctuation goes first, then
// the words, then the marks that were here before words were. The counts
// themselves are the last thing standing, because they are the answer.
func TestTheCountsGiveGroundInOrder(t *testing.T) {
	m := countModel(t, 10, 3)
	rows := m.workRowsFor("busy")
	if len(rows) != 13 {
		t.Fatalf("fixture resolved %d artifacts, want 13", len(rows))
	}
	for _, tc := range []struct {
		room int
		want string
	}{
		{40, "10 prs · 3 issues"},
		{17, "10 prs · 3 issues"},
		{16, "10 prs 3 issues"},
		{15, "10 prs 3 issues"},
		{14, "10pr 3is"},
		{8, "10pr 3is"},
		{7, "10p 3i"},
		{6, "10p 3i"},
		{5, ""},
	} {
		if got := ansi.Strip(workCounts(rows, tc.room)); got != tc.want {
			t.Errorf("workCounts(room=%d) = %q, want %q", tc.room, got, tc.want)
		}
	}
	// Below the room a count needs, the marks still say how much there is.
	badge := ansi.Strip(m.workBadge("busy", badgeSeparator+5))
	if !strings.Contains(badge, "+") {
		t.Errorf("badge = %q, want the mark summary to take over", badge)
	}
}

// One of a thing is one, not one of some things.
func TestTheCountsReadInTheSingular(t *testing.T) {
	for _, tc := range []struct {
		prs, tickets int
		want         string
	}{
		{1, 1, "1 pr · 1 issue"},
		{2, 1, "2 prs · 1 issue"},
		{1, 2, "1 pr · 2 issues"},
		{3, 0, "3 prs"},
		{0, 1, "1 issue"},
	} {
		m := countModel(t, tc.prs, tc.tickets)
		if got := ansi.Strip(workCounts(m.workRowsFor("busy"), 40)); got != tc.want {
			t.Errorf("%d prs and %d tickets read as %q, want %q", tc.prs, tc.tickets, got, tc.want)
		}
	}
}

// A forty-column rail is the narrowest this manager is used at, and what the
// counts have to do there is give ground without taking the row's own columns
// with them. One line per row at that width has nothing left after the state,
// the tool and the age, so the badge goes; the two-line density hands the meta
// its own line, and there the counts still fit -- in two letters a unit now
// that the badge follows the age rather than being spliced ahead of it, which
// left it a cell it used to spend on a second separator.
func TestTheSummaryReadsOnANarrowRail(t *testing.T) {
	tight := undecidedFleet(t, fleetSize, 40, 50)
	rail := ansi.Strip(railLinesText(tight.railLines(40, tight.listBodyHeight())))
	t.Logf("40-column rail, one line per row:\n%s", rail)
	if line := railRowLine(t, rail, "session-30"); !strings.Contains(line, "finished · claude") {
		t.Fatalf("the counts crowded the row's own columns: %q", line)
	}

	roomy := undecidedFleet(t, fleetSize, 40, 50)
	roomy.comfortableRows = true
	stacked := ansi.Strip(railLinesText(roomy.railLines(40, roomy.listBodyHeight())))
	t.Logf("40-column rail, two lines per row:\n%s", stacked)
	if !strings.Contains(stacked, "1pr 1is") {
		t.Fatalf("a forty-column rail counted nothing:\n%s", stacked)
	}

	wide := undecidedFleet(t, fleetSize, 120, 50)
	full := ansi.Strip(railLinesText(wide.railLines(64, wide.listBodyHeight())))
	t.Logf("64-column rail:\n%s", full)
	if !strings.Contains(full, "1 pr · 1 issue") {
		t.Fatalf("a rail with room for the words did not use them:\n%s", full)
	}
}
