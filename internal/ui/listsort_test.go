package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// sortFixture is three sessions whose stored order, activity order and name
// order are all different, so a test cannot pass by accident.
func sortFixture(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	now := time.Now()
	for i, sess := range []store.Session{
		{ID: "s-zulu", Name: "zulu", Status: status.Idle,
			CreatedAt: now.Add(-3 * time.Hour), LastStatusAt: now.Add(-time.Minute)},
		{ID: "s-alpha", Name: "alpha", Status: status.Idle,
			CreatedAt: now.Add(-2 * time.Hour), LastStatusAt: now.Add(-time.Hour)},
		{ID: "s-mike", Name: "mike", Status: status.Idle,
			CreatedAt: now.Add(-time.Hour), LastStatusAt: now.Add(-30 * time.Minute)},
	} {
		sess.Tool = "zsh"
		sess.Cwd = t.TempDir()
		if err := m.store.CreateSession(sess); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	loadStoredRows(t, m)
	return m
}

// The stored order is what the move keys wrote, and it is what the board
// shows until somebody asks for something else.
func TestManualIsTheDefaultOrder(t *testing.T) {
	m := sortFixture(t)
	if m.sortsList() {
		t.Fatal("a fresh board is not on the manual order")
	}
	if got, want := sessionNames(m), []string{"zulu", "alpha", "mike"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want the stored order %v", got, want)
	}
}

// Most recently active first, which is the order the complaint asked for.
func TestActivitySortPutsTheFreshestFirst(t *testing.T) {
	m := sortFixture(t)
	m.listSort = listSortActivity
	m.rebuildRows()
	if got, want := sessionNames(m), []string{"zulu", "mike", "alpha"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestNameSortIsAlphabetical(t *testing.T) {
	m := sortFixture(t)
	m.listSort = listSortName
	m.rebuildRows()
	if got, want := sessionNames(m), []string{"alpha", "mike", "zulu"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// The rail is rebuilt on every poll, so an order that is not total would
// reshuffle equal rows under a reaching hand.
func TestTheSortIsStableAcrossRebuilds(t *testing.T) {
	m := sortFixture(t)
	m.listSort = listSortActivity
	m.rebuildRows()
	first := sessionNames(m)
	for i := 0; i < 5; i++ {
		m.rebuildRows()
		if got := sessionNames(m); !slices.Equal(got, first) {
			t.Fatalf("rebuild %d reordered the board: %v then %v", i, first, got)
		}
	}
}

// Sessions that compare equal on activity still land in a fixed order,
// because every comparison ends on the id.
func TestEqualActivityStillHasATotalOrder(t *testing.T) {
	m := buildModel(t)
	at := time.Now().Add(-time.Hour)
	for _, id := range []string{"s-b", "s-a"} {
		if err := m.store.CreateSession(store.Session{
			ID: id, Name: id, Tool: "zsh", Cwd: t.TempDir(), Status: status.Idle,
			CreatedAt: at, LastStatusAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	loadStoredRows(t, m)
	m.listSort = listSortActivity
	m.rebuildRows()
	if got, want := sessionNames(m), []string{"s-a", "s-b"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want the id tie-break %v", got, want)
	}
}

// A launch lands on the freshest session rather than on root's rollup, which
// is what "open on what I was last doing" means once the order is activity.
func TestTheBoardOpensOnTheFreshestSession(t *testing.T) {
	m := sortFixture(t)
	m.listSort = listSortActivity
	// A launch, not a rebuild: no rows behind it and no cursor to restore,
	// which is the one case restoreCursor moves off root's rollup for.
	m.rows, m.cursor = nil, 0
	m.rebuildRows()
	sess, ok := m.selected()
	if !ok {
		t.Fatal("the board opened with no session selected")
	}
	if sess.Name != "zulu" {
		t.Errorf("opened on %q, want the most recently active (zulu)", sess.Name)
	}
}

// Triage has its own order and must not be overruled: the queue is ranked by
// who needs a person, not by who was touched last.
func TestTriageKeepsItsOwnOrder(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"busy": status.Working,
	})
	m.triage, m.listSort = true, listSortName
	m.rebuildRows()
	if got, want := sessionNames(m), []string{"ask", "busy"}; !slices.Equal(got, want) {
		t.Errorf("triage order = %v, want the queue's own order %v", got, want)
	}
}

// The move keys stay bound and say why they do nothing, rather than writing
// an order that would only surface if the sort were later set back to manual.
func TestTheMoveKeysRefuseUnderASort(t *testing.T) {
	m := sortFixture(t)
	m.listSort = listSortActivity
	m.rebuildRows()
	before := sessionNames(m)
	m.selectSessionRow(t, "mike")
	if _, cmd := m.reorderSelected(-1); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.errBar.text == "" {
		t.Error("the move key did nothing and said nothing")
	}
	m.rebuildRows()
	if got := sessionNames(m); !slices.Equal(got, before) {
		t.Errorf("the board moved anyway: %v then %v", before, got)
	}

	m.listSort = listSortManual
	m.errBar.text = ""
	m.selectSessionRow(t, "mike")
	if _, cmd := m.reorderSelected(-1); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.errBar.text != "" {
		t.Errorf("the move key refused on the manual order too: %q", m.errBar.text)
	}
}

// The choice survives the panel and the store, and an unknown stored value
// leaves the board in the order its owner arranged.
func TestListSortRoundTripsAndFailsSafe(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.settings.field = settingsFieldListSort
	seen := map[string]bool{}
	for range listSortModes {
		m.applyCmd(t, m.cycleSetting(1))
		seen[m.settings.listSort] = true
	}
	if !seen[listSortActivity] || !seen[listSortName] || !seen[listSortManual] {
		t.Fatalf("cycling did not reach every mode, only %v", seen)
	}
	for m.settings.listSort != listSortActivity {
		m.applyCmd(t, m.cycleSetting(1))
	}
	if _, cmd := m.saveAndCloseSettings(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.listSort != listSortActivity {
		t.Fatalf("model listSort %q, want %q", m.listSort, listSortActivity)
	}
	if got := storedListSort(m.store); got != listSortActivity {
		t.Fatalf("stored %q, want %q", got, listSortActivity)
	}

	if err := m.store.SetSetting(listSortSetting, "urgency"); err != nil {
		t.Fatal(err)
	}
	if got := storedListSort(m.store); got != listSortManual {
		t.Errorf("an unknown stored value read as %q, want %q", got, listSortManual)
	}
}

// The archive is read as a log: the last thing filed sits at the top, no
// matter what order the active list is in.
func TestArchivedViewIsNewestArchivedFirst(t *testing.T) {
	m := sortFixture(t)
	// Filed in the order zulu, mike, alpha: the reverse of the name order
	// and unlike the activity order, so the archive cannot inherit either.
	for _, id := range []string{"s-zulu", "s-mike", "s-alpha"} {
		if err := m.store.SetArchived(id, true); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	loadStoredRows(t, m)
	m.showArchived = true
	m.rebuildRows()
	if got, want := sessionNames(m), []string{"alpha", "mike", "zulu"}; !slices.Equal(got, want) {
		t.Errorf("archived order = %v, want newest archived first %v", got, want)
	}
	if m.listSortRefusal() == "" {
		t.Error("the move keys are live in the archived view")
	}
}
