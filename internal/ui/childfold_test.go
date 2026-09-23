package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// childModel is a parent that fanned out: three children in one group, plus
// an unrelated session to prove the fold reaches only the lineage it is
// about. Nothing here starts tmux — a fold, a badge and a queue order all
// have to be provable without a terminal.
func childModel(t testing.TB) *Model {
	t.Helper()
	m := &Model{
		width: 120, height: 34, mode: modeList,
		collapsed:  map[string]bool{},
		groups:     []string{"research"},
		groupPaths: map[string]string{"research": "/repos"},
		split:      splitState{ratio: defaultSplitRatio},
		preview:    previewSample,
	}
	m.cfg.Tools = map[string]config.Tool{"claude": {}}
	m.sessions = []store.Session{
		childSess("p1", "worker", "research", "", status.Working, time.Hour),
		childSess("c1", "probe-one", "research", "p1", status.Working, 50*time.Minute),
		childSess("c2", "probe-two", "research", "p1", status.Waiting, 40*time.Minute),
		childSess("c3", "probe-three", "research", "p1", status.Finished, 30*time.Minute),
		childSess("s9", "unrelated", "research", "", status.Idle, 20*time.Minute),
	}
	m.rebuildRows()
	return m
}

func childSess(id, name, group, parent, st string, age time.Duration) store.Session {
	now := time.Now()
	return store.Session{
		ID: id, Name: name, Group: group, ParentID: parent,
		Tool: "claude", Status: st, Cwd: "/repos/checkout",
		CreatedAt: now.Add(-age), LastStatusAt: now.Add(-age),
	}
}

// rowIDs is the session rows in order, so a test asserts a shape rather than
// an index nobody can read.
func rowIDs(m *Model) []string {
	var ids []string
	for _, row := range m.rows {
		if row.isSession() {
			ids = append(ids, row.sess.ID)
		}
	}
	return ids
}

func joined(ids []string) string { return strings.Join(ids, ",") }

func TestChildrenAreFoldedUntilSomebodySaysOtherwise(t *testing.T) {
	m := childModel(t)
	if got := joined(rowIDs(m)); got != "p1,s9" {
		t.Fatalf("rows = %q, want the parent and its sibling only: children fold by default", got)
	}
}

func TestUnfoldingAParentDrawsItsChildrenUnderIt(t *testing.T) {
	m := childModel(t)
	m.setChildrenFolded("p1", false)
	m.rebuildRows()
	if got := joined(rowIDs(m)); got != "p1,c1,c2,c3,s9" {
		t.Fatalf("rows = %q, want the children in creation order under their parent", got)
	}
	parentDepth := -1
	for _, row := range m.rows {
		if row.isSession() && row.sess.ID == "p1" {
			parentDepth = row.depth
		}
	}
	for _, row := range m.rows {
		if row.isSession() && row.sess.ParentID == "p1" && row.depth != parentDepth+1 {
			t.Fatalf("child %s drew at depth %d, want %d: one step in from its parent",
				row.sess.ID, row.depth, parentDepth+1)
		}
	}
}

func TestFoldingAParentAgainTakesTheChildrenBack(t *testing.T) {
	m := childModel(t)
	m.setChildrenFolded("p1", false)
	m.rebuildRows()
	m.setChildrenFolded("p1", true)
	m.rebuildRows()
	if got := joined(rowIDs(m)); got != "p1,s9" {
		t.Fatalf("rows = %q, want the children folded away again", got)
	}
}

// Arriving on a child from triage is the case: the queue draws every child,
// the cursor lands on one, and leaving the queue must not fold the row the
// cursor is standing on out from under it.
func TestAChildUnderTheCursorUnfoldsItsParent(t *testing.T) {
	m := childModel(t)
	m.triage = true
	m.rebuildRows()
	if !selectRowByID(m, "c2") {
		t.Fatal("triage did not draw the child to select")
	}
	m.triage = false
	m.rebuildRows()
	if got := joined(rowIDs(m)); !strings.Contains(got, "c2") {
		t.Fatalf("rows = %q, want the selected child still on screen", got)
	}
}

// selectRowByID puts the cursor on a session row, reporting whether it found
// one to put it on.
func selectRowByID(m *Model, id string) bool {
	for i, row := range m.rows {
		if row.isSession() && row.sess.ID == id {
			m.cursor = i
			return true
		}
	}
	return false
}

// The fold is a decision, so it outranks the cursor: a parent explicitly
// folded stays folded even while the cursor is on one of its children, or
// the fold key would be a door that will not shut.
func TestAnExplicitFoldOutranksTheCursor(t *testing.T) {
	m := childModel(t)
	m.setChildrenFolded("p1", true)
	m.railCursorSess = "c2"
	m.rebuildRows()
	if got := joined(rowIDs(m)); got != "p1,s9" {
		t.Fatalf("rows = %q, want the explicit fold to hold", got)
	}
}

func TestChildFoldsSurviveThroughTheCollapsedSet(t *testing.T) {
	m := childModel(t)
	m.setChildrenFolded("p1", false)
	if _, ok := m.collapsed[childFoldPrefix+"p1"]; !ok {
		t.Fatal("child fold is not in the collapsed set, so it cannot be persisted with the group folds")
	}
	if m.collapsed["research"] {
		t.Fatal("folding a parent's children folded its group")
	}
}

func TestChildBadgeCountsByStatus(t *testing.T) {
	m := childModel(t)
	got := m.childBadgeText(m.sessions, "p1", 0)
	want := childGlyph + "3 · " + statusGlyph(status.Working) + "1 " +
		statusGlyph(status.Waiting) + "1 " + statusGlyph(status.Finished) + "1"
	if got != want {
		t.Fatalf("childBadgeText = %q, want %q", got, want)
	}
}

func TestChildBadgeCountsArchivedChildrenSeparately(t *testing.T) {
	m := childModel(t)
	if got := m.childBadgeText(m.sessions, "p1", 2); !strings.HasSuffix(got, "(+2 done)") {
		t.Fatalf("childBadgeText = %q, want a trailing (+2 done)", got)
	}
}

// An archived child is not a live one: counting it twice would make the badge
// grow every time the sweep ran.
func TestChildBadgeLeavesArchivedRowsOutOfTheLiveCount(t *testing.T) {
	m := childModel(t)
	m.sessions[1].Archived = true
	if got := m.childBadgeText(m.sessions, "p1", 1); !strings.HasPrefix(got, childGlyph+"2 ") {
		t.Fatalf("childBadgeText = %q, want a live count of 2", got)
	}
}

func TestChildBadgeIsEmptyForASessionThatSpawnedNothing(t *testing.T) {
	m := childModel(t)
	if got := m.childBadgeText(m.sessions, "s9", 0); got != "" {
		t.Fatalf("childBadgeText = %q, want nothing on a session with no children", got)
	}
}

// The rows are the summary once they are drawn, so the count comes off.
func TestTheBadgeGivesWayToTheRowsItSummarises(t *testing.T) {
	m := childModel(t)
	m.setChildrenFolded("p1", false)
	m.rebuildRows()
	if m.childBadge("p1", 80) != "" {
		t.Fatal("badge stayed on an unfolded parent, duplicating the rows under it")
	}
	m.archivedChildren = map[string]int{"p1": 2}
	if m.childBadge("p1", 80) == "" {
		t.Fatal("badge went away while it still had archived children to report")
	}
}

func TestChildBadgeGivesWayWhenTheRowHasNoRoom(t *testing.T) {
	m := childModel(t)
	if m.childBadge("p1", 2) != "" {
		t.Fatal("badge drew into a row that had no room for it")
	}
}

// The queue is the one place a child must never be buried: a parent still
// working while its child sits on a question has to rise to the question.
func TestTriageLiftsAParentToItsWaitingChild(t *testing.T) {
	parent := childSess("p1", "worker", "research", "", status.Working, time.Hour)
	waiting := childSess("c1", "probe", "research", "p1", status.Waiting, 30*time.Minute)
	idle := childSess("s9", "other", "research", "", status.Idle, 2*time.Hour)
	queue := []store.Session{idle, parent}
	kids := map[string][]store.Session{"p1": {waiting}}
	(&Model{}).sortTriageWithChildren(queue, kids)
	if queue[0].ID != "p1" {
		t.Fatalf("queue leads with %s, want the parent of the waiting child", queue[0].ID)
	}
}

// Without a child needing anything, the parent keeps its own tier: the lift
// must not promote every parent on the board.
func TestTriageLeavesAParentWhoseChildrenAreQuiet(t *testing.T) {
	parent := childSess("p1", "worker", "research", "", status.Working, time.Hour)
	working := childSess("c1", "probe", "research", "p1", status.Working, 30*time.Minute)
	idle := childSess("s9", "other", "research", "", status.Idle, 2*time.Hour)
	queue := []store.Session{parent, idle}
	kids := map[string][]store.Session{"p1": {working}}
	(&Model{}).sortTriageWithChildren(queue, kids)
	if queue[0].ID != "s9" {
		t.Fatalf("queue leads with %s, want the idle session: idle outranks working", queue[0].ID)
	}
}

func TestTriageDrawsAChildDirectlyUnderItsParent(t *testing.T) {
	m := childModel(t)
	m.triage = true
	m.rebuildRows()
	ids := rowIDs(m)
	var at = -1
	for i, id := range ids {
		if id == "p1" {
			at = i
		}
	}
	if at < 0 || at+1 >= len(ids) || ids[at+1] != "c1" {
		t.Fatalf("triage rows = %q, want the parent's first child immediately after it", joined(ids))
	}
}

// Triage and search are opened to find a session, so the fold does not apply
// there: a child blocked on a person is what they exist to surface.
func TestTriageDrawsChildrenThroughAFold(t *testing.T) {
	m := childModel(t)
	m.setChildrenFolded("p1", true)
	m.triage = true
	m.rebuildRows()
	if got := joined(rowIDs(m)); !strings.Contains(got, "c2") {
		t.Fatalf("triage rows = %q, want the folded children drawn anyway", got)
	}
}

func TestSearchDrawsChildrenThroughAFold(t *testing.T) {
	m := childModel(t)
	m.setChildrenFolded("p1", true)
	m.search = "probe"
	m.rebuildRows()
	if got := joined(rowIDs(m)); !strings.Contains(got, "c1") {
		t.Fatalf("search rows = %q, want the matching children drawn through the fold", got)
	}
}

// F's direction is decided by whether anything is open, and an unfolded
// parent counts: without this arm F would read a board as fully collapsed
// and only ever shut.
func TestFoldAllSeesAnUnfoldedParent(t *testing.T) {
	m := childModel(t)
	// The group has to be folded first, or its own arm answers for the
	// board and the child fold is never reached.
	m.collapsed["research"] = true
	if !m.allFoldsCollapsed() {
		t.Fatal("a board with everything folded did not read as collapsed")
	}
	m.setChildrenFolded("p1", false)
	if m.allFoldsCollapsed() {
		t.Fatal("an unfolded parent read as fully collapsed, so F would only ever shut")
	}
}

// A terminal under a session is that session's own shell, opened with T and
// expected on screen. Folding it away broke every existing test that spawns
// one and looks for the cursor on it.
func TestShellsUnderASessionNeverFoldAway(t *testing.T) {
	m := childModel(t)
	m.cfg.Tools["terminal"] = config.Tool{Shell: true}
	shell := childSess("sh1", "term-one", "research", "p1", status.Idle, 10*time.Minute)
	shell.Tool = "terminal"
	m.sessions = append(m.sessions, shell)
	m.rebuildRows()
	if got := joined(rowIDs(m)); !strings.Contains(got, "sh1") {
		t.Fatalf("rows = %q, want the shell drawn through the fold", got)
	}
	if strings.Contains(joined(rowIDs(m)), "c1") {
		t.Fatalf("rows = %q, want the spawned agents still folded", joined(rowIDs(m)))
	}
}

// A session whose only child is its own shell has not fanned out, so it
// carries no summary and nothing to fold.
func TestASessionWithOnlyAShellHasNothingToFold(t *testing.T) {
	m := childModel(t)
	m.cfg.Tools["terminal"] = config.Tool{Shell: true}
	shell := childSess("sh1", "term-one", "research", "s9", status.Idle, 10*time.Minute)
	shell.Tool = "terminal"
	m.sessions = append(m.sessions, shell)
	if m.hasChildren("s9") {
		t.Fatal("a session with only a shell reads as foldable")
	}
	if got := m.childBadgeText(m.sessions, "s9", 0); got != "" {
		t.Fatalf("childBadgeText = %q, want no badge for a shell", got)
	}
}

// A lifted parent is standing in for a child, so it queues on that child's
// wait. Ordering it by its own LastStatusAt would rank the question by how
// long the parent had been working, which is nobody's wait.
func TestTriageOrdersALiftedParentByItsChildsWait(t *testing.T) {
	// Parent A has been working for four hours but its child has only just
	// asked; parent B has been working ten minutes and its child asked an
	// hour ago. B's question is the older one and goes first.
	parentA := childSess("pa", "older-parent", "research", "", status.Working, 4*time.Hour)
	kidA := childSess("ka", "fresh-question", "research", "pa", status.Waiting, time.Minute)
	parentB := childSess("pb", "newer-parent", "research", "", status.Working, 10*time.Minute)
	kidB := childSess("kb", "old-question", "research", "pb", status.Waiting, time.Hour)
	queue := []store.Session{parentA, parentB}
	kids := map[string][]store.Session{"pa": {kidA}, "pb": {kidB}}
	(&Model{}).sortTriageWithChildren(queue, kids)
	if queue[0].ID != "pb" {
		t.Fatalf("queue leads with %s, want pb: its child has been waiting longest", queue[0].ID)
	}
}

// The 60-column list is the default at a 200-column terminal, and a long
// name and the badges beside it can fill it on their own. The fan-out must
// still be countable there: the badge gives up its breakdown, not its existence.
func TestChildBadgeDegradesToTheCountBeforeItIsDropped(t *testing.T) {
	m := childModel(t)
	full := m.childBadgeText(m.sessions, "p1", 0)
	room := lipgloss.Width(full) + badgeSeparator - 1
	got := ansi.Strip(m.childBadge("p1", room))
	if got != badgeGap+childGlyph+"3" {
		t.Fatalf("childBadge at %d cells = %q, want the bare count %q",
			room, got, badgeGap+childGlyph+"3")
	}
}

// Down to the narrowest room that fits it, the count is still there.
func TestChildBadgeKeepsTheCountAtItsNarrowest(t *testing.T) {
	m := childModel(t)
	want := badgeGap + childGlyph + "3"
	got := ansi.Strip(m.childBadge("p1", lipgloss.Width(childGlyph+"3")+badgeSeparator))
	if got != want {
		t.Fatalf("childBadge at its narrowest = %q, want %q", got, want)
	}
}

// Every other badge on the row wears a two-cell gap. Without it the badge
// before it ran straight into the arrow: "40s↳1".
func TestChildBadgeWearsTheBadgeGap(t *testing.T) {
	m := childModel(t)
	got := ansi.Strip(m.childBadge("p1", 80))
	if !strings.HasPrefix(got, badgeGap) {
		t.Fatalf("childBadge = %q, want it to open with the badge gap", got)
	}
	if strings.HasPrefix(strings.TrimPrefix(got, badgeGap), " ") {
		t.Fatalf("childBadge = %q, want exactly one gap", got)
	}
}

// The ladder sheds the breakdown before the archived tally, and the count
// outlives both.
func TestChildBadgeLadderGivesGroundInOrder(t *testing.T) {
	m := childModel(t)
	m.archivedChildren = map[string]int{"p1": 2}
	ladder := m.childBadgeLadder("p1")
	if len(ladder) < 2 {
		t.Fatalf("ladder = %v, want more than one rung", ladder)
	}
	if !strings.Contains(ladder[0], "(+2 done)") || !strings.Contains(ladder[0], statusGlyph(status.Waiting)) {
		t.Fatalf("widest rung = %q, want the full form", ladder[0])
	}
	last := ladder[len(ladder)-1]
	if last != childGlyph+"3" {
		t.Fatalf("narrowest rung = %q, want the bare count", last)
	}
	for i := 1; i < len(ladder); i++ {
		if lipgloss.Width(ladder[i]) >= lipgloss.Width(ladder[i-1]) {
			t.Fatalf("ladder does not narrow: %q then %q", ladder[i-1], ladder[i])
		}
	}
}

// The child badge has to survive on a session row at sixty columns, the
// default list width: a folded fan-out must not be invisible.
func TestASixtyColumnRowCarriesTheChildBadge(t *testing.T) {
	m := childModel(t)
	m.width = 60
	m.rebuildRows()
	var row string
	for i, entry := range m.rows {
		if entry.isSession() && entry.sess.ID == "p1" {
			row = ansi.Strip(m.renderTreeRow(entry, false, 60, i, panelHex()))
			break
		}
	}
	if row == "" {
		t.Fatal("no parent row rendered")
	}
	if !strings.Contains(row, childGlyph) {
		t.Fatalf("row = %q, want the child badge: a folded fan-out must not be invisible", row)
	}
	at := strings.Index(row, childGlyph)
	if at > 0 && row[at-1] != ' ' {
		t.Fatalf("row = %q, want a separator before the child badge", row)
	}
}

func TestDepthCapFlattensADeeperLineage(t *testing.T) {
	if childDepthCap != 3 {
		t.Fatalf("childDepthCap = %d, want 3", childDepthCap)
	}
}
