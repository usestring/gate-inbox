package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
)

func sessionID(t *testing.T, m *Model, name string) string {
	t.Helper()
	for _, sess := range m.sessions {
		if sess.Name == name {
			return sess.ID
		}
	}
	t.Fatalf("no session named %q", name)
	return ""
}

// A raised session never jumps a more pressing state: an urgent finished
// session waits behind every question and error, and an urgent idle session
// behind every session that needs a person. The rail has to read in the order
// the drain walks.
func TestPriorityLiftsWithinTheTriageBucket(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	if err := m.store.SetPriority(sessionID(t, m, "reviewme"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "napping"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))

	want := []string{"old-block", "new-block", "crashed", "reviewme", "napping", "grinder", "booting", "gone"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage order = %v want %v", got, want)
	}
	rail := triageRail(m)
	if !strings.Contains(rail, "reviewme "+priority.Urgent.Glyph()) {
		t.Fatalf("the urgent row wears no glyph:\n%s", rail)
	}
	assertWidelyDrawn(t, "priority rail", rail)
}

// A group's tier covers its subtree, so every session filed under it goes
// first among the sessions in its state, and a session moved out of the
// group sheds it with the group.
func TestGroupPriorityCoversTheSubtree(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	if err := m.store.SetGroupPriority("beta", priority.Urgent); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))

	want := []string{"new-block", "old-block", "crashed", "reviewme", "napping", "grinder", "booting", "gone"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage order = %v want %v", got, want)
	}
}

// The drain reaches a raised session before the ring carries on from where
// it left off, which is the whole point of raising one mid-drain -- but only
// among the sessions in its state: a raised finished session still waits for
// the error ahead of it.
func TestTriageDrainHandsOverPriorityFirst(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"done":  status.Finished,
		"rest":  status.Idle,
		"spare": status.Idle,
	})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	// Raised while the operator is inside ask, so it is nowhere near the
	// ring's next step; the row order is what the previous build left.
	if err := m.store.SetPriority(sessionID(t, m, "done"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	for i := range m.sessions {
		if m.sessions[i].Name == "done" || m.sessions[i].Name == "spare" {
			m.sessions[i].Priority = priority.Urgent
		}
	}
	m.rebuildRows()
	for _, want := range []string{"broke", "done", "spare", "rest"} {
		updated, _ := m.handleFocusKey(ctrlQ())
		m = updated.(*Model)
		if m.mode != modeFocus {
			t.Fatalf("ctrl+q dropped out of the queue before %q: %s", want, m.errBar.text)
		}
		if got := focusedName(t, m); got != want {
			t.Fatalf("ctrl+q landed on %q want %q", got, want)
		}
	}
}

// p walks the whole cycle on the store and the rail together, one press at
// a time, and comes back to no tier at all -- the only way to clear one.
func TestPriorityKeyCyclesTheSession(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	id := sessionID(t, m, "napping")

	for _, want := range []priority.Tier{priority.Urgent, priority.High, priority.Medium, priority.Low, priority.Unset} {
		m.selectSessionRow(t, "napping")
		pressKey(t, m, key("p"))
		got, err := m.store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Priority != want {
			t.Fatalf("p stored %q, want %q", got.Priority, want)
		}
		rail := triageRail(m)
		if want == priority.Unset {
			if strings.Contains(rail, "napping "+priority.Low.Glyph()) {
				t.Fatalf("the glyph outlived the tier:\n%s", rail)
			}
			continue
		}
		if !strings.Contains(rail, "napping "+want.Glyph()) {
			t.Fatalf("%q waited for a poll:\n%s", want, rail)
		}
	}
}

// The tiers order among themselves inside each state, so the queue reads
// urgent, high, then the ones nobody has tiered, then low -- and a tier never
// carries a session past a more pressing state.
func TestTiersOrderWithinTheBucket(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	if err := m.store.SetPriority(sessionID(t, m, "old-block"), priority.Low); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "new-block"), priority.High); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "crashed"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "reviewme"), priority.High); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))

	want := []string{"new-block", "old-block", "crashed", "reviewme", "napping", "grinder", "booting", "gone"}
	if got := sessionNames(m); !slices.Equal(got, want) {
		t.Fatalf("triage order = %v want %v", got, want)
	}
}

// p on a group row tiers the group; root refuses, since it is every
// session and a tier on everything ranks nothing.
func TestPriorityKeyTogglesTheGroupAndRefusesRoot(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectGroupRow(t, "alpha")
	pressKey(t, m, key("p"))
	groups, err := m.store.Groups()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Name == "alpha" && g.Priority != priority.Urgent {
			t.Fatalf("p on the group row stored %q, want urgent", g.Priority)
		}
	}
	rail := triageRail(m)
	if !strings.Contains(rail, "alpha "+priority.Urgent.Glyph()) {
		t.Fatalf("group row wears no glyph:\n%s", rail)
	}
	if !strings.Contains(rail, "old-block "+priority.Urgent.Glyph()) {
		t.Fatalf("session under a tiered group wears no glyph:\n%s", rail)
	}

	m.selectGroupRow(t, rootGroup)
	pressKey(t, m, key("p"))
	if m.errBar.text == "" {
		t.Fatal("p on root said nothing")
	}
}

// A tier ranks a session among the sessions in its state and never past a
// more pressing one: turning triage on hands over the question first, even
// with an urgent finished session on the board, and the rail agrees.
func TestARaisedTierNeverJumpsAMorePressingState(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"done":  status.Finished,
		"rest":  status.Idle,
	})
	if err := m.store.SetPriority(sessionID(t, m, "done"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPriority(sessionID(t, m, "rest"), priority.Urgent); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	pressKey(t, m, key("i"))

	if got, want := sessionNames(m), []string{"ask", "broke", "done", "rest"}; !slices.Equal(got, want) {
		t.Fatalf("triage order = %v want %v", got, want)
	}
	if m.mode != modeFocus {
		t.Fatalf("turning triage on entered nothing: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("the queue opened on %q want ask", got)
	}
	for _, want := range []string{"broke", "done", "rest"} {
		updated, _ := m.handleFocusKey(ctrlQ())
		m = updated.(*Model)
		if m.mode != modeFocus {
			t.Fatalf("ctrl+q dropped out of the queue before %q: %s", want, m.errBar.text)
		}
		if got := focusedName(t, m); got != want {
			t.Fatalf("ctrl+q landed on %q want %q", got, want)
		}
	}
}
