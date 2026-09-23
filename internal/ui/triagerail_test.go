package ui

import "testing"

// A board that comes up with triage already on runs the flat build first and
// never runs the tree build at all, so the flat build is the only chance the
// fold column gets to be counted. It used to be counted only under a search,
// which left a fresh triage model reserving nothing while its rows plainly
// had work to fold, and every session's second line two columns off. A
// non-triage build usually runs first and hides this.
func TestTriageAtStartupReservesTheFoldColumn(t *testing.T) {
	m := fleetModel(t, 87, 200, 50)
	if !m.railWorkColumn {
		t.Fatal("the fleet fixture should have work to fold")
	}
	// A model whose first build is the triage one has never had the column
	// set by anything, which is the state the zero value leaves it in.
	m.railWorkColumn = false
	m.triage = true
	m.rebuildRows()
	if !m.anyRailWork(m.rows) {
		t.Fatal("triage keeps every session, so these rows still have work to fold")
	}
	if !m.railWorkColumn {
		t.Fatal("triage's first build left the fold column unreserved")
	}
}

// The other direction, so the fix cannot be "always reserve it": triage over
// a set with nothing to fold has to give the column back the same way a
// search does.
func TestTriageWithNothingToFoldDropsTheFoldColumn(t *testing.T) {
	m := fleetModel(t, 87, 200, 50)
	m.triage = true
	// Dropping the tracker's discoveries is what leaves the rows with no
	// work, without changing which sessions triage is handed.
	m.work = nil
	m.rebuildRows()
	if m.anyRailWork(m.rows) {
		t.Fatal("clearing the tracker should leave no work to fold")
	}
	if m.railWorkColumn {
		t.Fatal("triage over rows with nothing to fold should drop the column")
	}
}
