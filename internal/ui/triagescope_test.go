package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// seedNested adds a session inside a subgroup of an existing one, which is
// what separates "this group" from "this group and everything under it".
func seedNested(t *testing.T, m *Model, id, name, group, st string) {
	t.Helper()
	base := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	sess := store.Session{
		ID: id, Name: name, Group: group, Status: st,
		Tool: "claude", Cwd: "/tmp",
		CreatedAt: base, LastStatusAt: base.Add(30 * time.Second),
	}
	if err := m.store.CreateSession(sess); err != nil {
		t.Fatalf("create session %q: %v", id, err)
	}
	loadStoredRows(t, m)
}

// Triage is walked from inside a group far more often than across the whole
// board, so the queue it builds is the group the cursor was standing in.
func TestTriageScopesToTheCursorsGroup(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "grinder")

	updated, cmd := m.handleKey(key("i"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.triageScope != "alpha" {
		t.Fatalf("triage scope = %q want alpha", m.triageScope)
	}
	want := []string{"old-block", "grinder"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage queue = %v want %v", got, want)
	}
	// The rail has flattened the groups away, so the badge is the only
	// thing left that can say which group is being drained.
	if rail := triageRail(m); !strings.Contains(rail, "TRIAGE ALPHA") {
		t.Fatalf("the rail does not name the group triage is scoped to:\n%s", rail)
	}
}

// The scope is a subtree: a session filed one level down is part of the work
// the operator is doing, not a different queue.
func TestTriageScopeIncludesSubgroups(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	seedNested(t, m, "s9", "nested-block", "alpha/deep", status.Waiting)
	m.selectGroupRow(t, "alpha")

	updated, cmd := m.handleKey(key("i"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	want := []string{"old-block", "nested-block", "grinder"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage queue = %v want %v", got, want)
	}
}

// A cursor outside every group is asking for all of them: root and the
// sessions filed loose under it keep the fleet-wide drain reachable.
func TestTriageFromRootDrainsTheWholeFleet(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "reviewme")

	updated, cmd := m.handleKey(key("i"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.triageScope != "" {
		t.Fatalf("triage scope = %q want the whole fleet", m.triageScope)
	}
	want := []string{"old-block", "new-block", "crashed", "reviewme", "napping", "grinder", "booting", "gone"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage queue = %v want %v", got, want)
	}
}

// Leaving triage puts the tree back whole, so the scope cannot survive to
// narrow the next drain the operator asks for from somewhere else.
func TestLeavingTriageClearsTheScope(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "crashed")

	for range 2 {
		updated, cmd := m.handleKey(key("i"))
		m = updated.(*Model)
		if cmd != nil {
			m.applyCmd(t, cmd)
		}
	}
	if m.triage {
		t.Fatal("the second i did not leave triage")
	}
	if m.triageScope != "" {
		t.Fatalf("triage scope = %q after leaving triage", m.triageScope)
	}
	if got := len(sessionNames(m)); got != 8 {
		t.Fatalf("the tree came back with %d sessions, want the whole fleet", got)
	}
}

// The advance is what ctrl+q walks, and it has to stop at the edge of the
// scope rather than hand over a session from work the operator is not doing.
func TestTriageAdvanceStopsAtTheScopeEdge(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "new-block")

	updated, cmd := m.handleKey(key("i"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	index, ok := m.nextTriageInput("s3", map[string]bool{})
	if !ok {
		t.Fatal("the errored session in the same group should be next")
	}
	if got := m.rows[index].sess.Name; got != "crashed" {
		t.Fatalf("next = %q want crashed", got)
	}
	// reviewme is finished and would be next in a fleet-wide drain; it is
	// in no group, so a drain of beta is over here.
	if _, ok := m.nextTriageInput("s3", map[string]bool{"s4": true}); ok {
		t.Fatal("the drain walked out of the group it was scoped to")
	}
}

// A scope naming a group that is gone -- deleted, renamed, or restored from
// a setting older than the board -- would drain an empty rail with nothing
// on screen to say why, so the build widens it back to the fleet.
func TestTriageScopeForAMissingGroupWidensToTheFleet(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.triage = true
	m.triageScope = "ghost"
	m.rebuildRows()

	if m.triageScope != "" {
		t.Fatalf("triage scope = %q want it dropped", m.triageScope)
	}
	if got := len(sessionNames(m)); got != 8 {
		t.Fatalf("the widened queue holds %d sessions, want the whole fleet", got)
	}
}

// The footers are what a user reads while the rail is off screen or its
// badge is out of view, so a scoped queue has to name its group in both:
// the list footer beside the key that leaves, and the focused footer beside
// the key that walks on.
func TestTheTriageFootersNameTheScopedGroup(t *testing.T) {
	m := shotModel()
	m.width, m.height = 200, 50
	m.triage = true
	m.triageScope = "backend"

	list := ansi.Strip(m.peekLegend(m.listBodyHeight()))
	if !strings.Contains(list, "triage: backend") {
		t.Errorf("the list footer does not name the group being drained:\n%s", list)
	}

	m.mode = modeFocus
	focus := ansi.Strip(m.viewFooter())
	if !strings.Contains(focus, "next in backend") {
		t.Errorf("the focused footer does not say where ctrl+q goes next:\n%s", focus)
	}
	if !strings.Contains(focus, "stop triage") {
		t.Errorf("the focused footer lost its way out of triage:\n%s", focus)
	}
}

// A fleet-wide drain has no group to name, so both footers keep the wording
// they had before triage could be scoped.
func TestTheTriageFootersKeepTheirWordingUnscoped(t *testing.T) {
	m := shotModel()
	m.width, m.height = 200, 50
	m.triage = true

	list := ansi.Strip(m.viewFooter())
	if !strings.Contains(list, "back to groups") {
		t.Errorf("the list footer lost its exit hint:\n%s", list)
	}
	if strings.Contains(list, "triage: ") {
		t.Errorf("an unscoped drain named a group anyway:\n%s", list)
	}

	m.mode = modeFocus
	focus := ansi.Strip(m.viewFooter())
	if !strings.Contains(focus, "next needing input") {
		t.Errorf("the focused footer lost its advance hint:\n%s", focus)
	}
}

// Triage draws one queue, not a tree, so the pinned root row has nothing
// left to head: over a queue drawn from one group it reads as that queue's
// own label and says the wrong group, and over a fleet-wide drain it counts
// only the ungrouped sessions while the rail below it holds every session
// there is. A search drops it for the same reason.
func TestTriageDropsTheRootRow(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	if len(m.rows) == 0 || !m.rows[0].isRoot() {
		t.Fatal("the tree should still be headed by root outside triage")
	}
	m.selectSessionRow(t, "grinder")

	updated, cmd := m.handleKey(key("i"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	for i, row := range m.rows {
		if row.isRoot() {
			t.Fatalf("row %d is still root's rollup in a queue scoped to %q", i, m.triageScope)
		}
	}
	if rail := triageRail(m); strings.Contains(rail, "root") {
		t.Fatalf("the triage rail still paints a root row:\n%s", rail)
	}
	// Leaving triage is what puts the tree back, root included.
	updated, cmd = m.handleKey(key("i"))
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	if len(m.rows) == 0 || !m.rows[0].isRoot() {
		t.Fatal("leaving triage did not bring root's rollup back")
	}
}
