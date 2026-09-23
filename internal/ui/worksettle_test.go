package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/worktracker"
)

// settledTreeModel is workTreeModel with a clock the test moves. sample-repo-5 is on
// a merged pull request and a started ticket, sample-repo-11 on a merged pull
// request alone, sample-repo-21 on one that needs a person.
func settledTreeModel(t *testing.T) (*Model, *time.Time) {
	t.Helper()
	m := workTreeModel(t)
	now := time.Unix(1700000000, 0)
	m.work.Now = func() time.Time { return now }
	return m, &now
}

// unfoldAll opens every session's work on the rail, which is what sights the
// rows under all of them at once.
func unfoldAll(t *testing.T, m *Model) {
	t.Helper()
	for _, sess := range m.sessions {
		m.setWorkFolded(sess.ID, false)
	}
	m.rebuildRows()
}

// The rule in one test: a merged pull request the operator had on screen
// leaves the rail and the counts a day later, and does not come back.
func TestASettledArtifactLeavesTheBoardADayAfterItWasSeen(t *testing.T) {
	m, now := settledTreeModel(t)

	// Hanging the rows off the rail is the sighting; nothing goes on sight.
	unfoldAll(t, m)
	if labels := m.workLabels("s2"); len(labels) != 1 {
		t.Fatalf("sample-repo-11 lost its pull request on sight: %v", labels)
	}

	*now = now.Add(worktracker.DefaultSettleAfter)
	m.runWork(t)

	if labels := m.workLabels("s2"); len(labels) != 0 {
		t.Fatalf("sample-repo-11's merged pull request is still on it: %v", labels)
	}
	if labels := m.workLabels("s1"); len(labels) != 1 || !strings.HasPrefix(labels[0], "ABC-133683") {
		t.Fatalf("sample-repo-5 should keep only its started ticket: %v", labels)
	}
	if m.workLabels("s3")[0] != "example-org/sample-repo#700 (open ✕ · changes requested)" {
		t.Fatalf("a pull request that needs a person moved: %v", m.workLabels("s3"))
	}

	if badge := ansi.Strip(m.workBadge("s2", 40)); badge != "" {
		t.Errorf("the badge still counts settled work: %q", badge)
	}
	if m.hasRailWork(m.sessions[1]) {
		t.Error("the rail still folds a session on nothing but settled work")
	}
	// And it is gone for good: showing everything is not showing history.
	m.showAllWork = true
	m.rebuildRows()
	if labels := m.workLabels("s2"); len(labels) != 0 {
		t.Errorf("W brought settled work back: %v", labels)
	}
}

// A sighting is the operator's chance to see the row. A folded session shows
// marks, not rows, so it never starts the clock.
func TestOnlyRowsOnAnOpenScreenAreSighted(t *testing.T) {
	m, now := settledTreeModel(t)
	for _, sess := range m.sessions {
		m.setWorkFolded(sess.ID, sess.ID == "s2")
	}
	m.rebuildRows()

	*now = now.Add(2 * worktracker.DefaultSettleAfter)
	m.runWork(t)
	if labels := m.workLabels("s2"); len(labels) != 1 {
		t.Fatalf("a folded session's pull request was sighted through the fold: %v", labels)
	}
	if labels := m.workLabels("s1"); len(labels) != 1 {
		t.Fatalf("the open session's merged pull request did not retire: %v", labels)
	}

	// Unfolding is a sighting; the clock runs from there.
	m.setWorkFolded("s2", false)
	m.rebuildRows()
	*now = now.Add(worktracker.DefaultSettleAfter - time.Minute)
	m.runWork(t)
	if labels := m.workLabels("s2"); len(labels) != 1 {
		t.Fatalf("retired before a day on screen: %v", labels)
	}
	*now = now.Add(2 * time.Minute)
	m.runWork(t)
	if labels := m.workLabels("s2"); len(labels) != 0 {
		t.Fatalf("a day after unfolding it is still there: %v", labels)
	}
}

// The rail is the other screen rows appear on: the cursor landing on a
// session opens its work, and that is a sighting too.
func TestTheRailSightsTheRowsItExpands(t *testing.T) {
	m, now := settledTreeModel(t)
	// The cursor starts on sample-repo-5, and a cursor is a sighting: fold it by
	// hand so it is the session the rail never opens.
	m.setWorkFolded("s1", true)
	m.rebuildRows()
	for i, row := range m.rows {
		if row.isSession() && row.sess.ID == "s2" {
			m.cursor = i
		}
	}
	m.rebuildRows()
	if !m.railWorkExpanded("s2") {
		t.Fatal("the cursor's session did not open its work")
	}

	*now = now.Add(worktracker.DefaultSettleAfter)
	m.runWork(t)
	if labels := m.workLabels("s2"); len(labels) != 0 {
		t.Fatalf("the rail's sighting did not count: %v", labels)
	}
	if labels := m.workLabels("s1"); len(labels) != 2 {
		t.Fatalf("a session the rail never opened lost a row: %v", labels)
	}
}
