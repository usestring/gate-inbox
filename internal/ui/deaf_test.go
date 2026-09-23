package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// unansweredKeys plays n keystrokes at a session whose pane answers none of them:
// every chase comes back with the frame it started from, which is what the
// wedged pane on 2026-09-10 did to every key the operator pressed.
func unansweredKeys(m *Model, sess store.Session, n int) {
	for range n {
		m.noteEcho(sess, true, false)
	}
}

func TestARunOfUnansweredKeysMarksThePaneDeaf(t *testing.T) {
	m := &Model{}
	sess := store.Session{ID: "a1", Name: "cold-start-performance", Status: status.Waiting}

	unansweredKeys(m, sess, deafKeyRun-1)
	if m.isDeaf(sess) {
		t.Fatalf("%d unanswered keys marked the pane deaf; only %d should", deafKeyRun-1, deafKeyRun)
	}
	if m.errBar.text != "" {
		t.Fatalf("error bar spoke early: %q", m.errBar.text)
	}

	unansweredKeys(m, sess, 1)
	if !m.isDeaf(sess) {
		t.Fatalf("%d unanswered keys left the pane unmarked", deafKeyRun)
	}
	if !strings.Contains(m.errBar.text, sess.Name) {
		t.Fatalf("hint %q does not name the session", m.errBar.text)
	}
}

// The count is a run, not a tally. A key an agent legitimately discards is
// normal -- what is not normal is a pane that never paints at all -- so any
// repaint has to put the run back to zero.
func TestOneRepaintClearsTheRun(t *testing.T) {
	m := &Model{}
	sess := store.Session{ID: "a1", Name: "ask", Status: status.Waiting}

	unansweredKeys(m, sess, deafKeyRun-1)
	m.noteEcho(sess, true, true)
	unansweredKeys(m, sess, deafKeyRun-1)

	if m.isDeaf(sess) {
		t.Fatalf("a repaint in the middle of the run did not clear it")
	}
}

// A tick frame that finds nothing changed is the ordinary state of a session
// waiting for an answer. Only a keystroke's own chase can say a key came to
// nothing, so only a chase may count.
func TestAnIdleTickIsNotEvidenceOfADeafPane(t *testing.T) {
	m := &Model{}
	sess := store.Session{ID: "a1", Name: "ask", Status: status.Waiting}

	for range deafKeyRun * 3 {
		m.noteEcho(sess, false, false)
	}
	if m.isDeaf(sess) {
		t.Fatalf("ticks at an idle pane marked it deaf")
	}
}

// Runs do not carry between panes: leaving one session half way through a
// run and typing into another must not add the two together.
func TestTheRunBelongsToOnePane(t *testing.T) {
	m := &Model{}
	first := store.Session{ID: "a1", Name: "first", Status: status.Waiting}
	second := store.Session{ID: "a2", Name: "second", Status: status.Waiting}

	unansweredKeys(m, first, deafKeyRun-1)
	unansweredKeys(m, second, deafKeyRun-1)

	if m.isDeaf(first) || m.isDeaf(second) {
		t.Fatalf("a run split across two panes marked one of them deaf")
	}
}

// A repaint is proof of life wherever it comes from, so it also lifts a mark
// already made -- the board must not go on calling a revived pane deaf.
func TestARepaintLiftsAnExistingMark(t *testing.T) {
	m := &Model{}
	sess := store.Session{ID: "a1", Name: "ask", Status: status.Waiting}

	unansweredKeys(m, sess, deafKeyRun)
	if !m.isDeaf(sess) {
		t.Fatalf("the pane was never marked")
	}
	m.noteEcho(sess, true, true)
	if m.isDeaf(sess) {
		t.Fatalf("the mark survived a repaint")
	}
}

// The mark carries the state it was made in, so it lapses on its own when the
// poller sees the pane move -- which is what a kill and revive produces, and
// is the only repair for a wedged agent.
func TestTheMarkLapsesWhenTheSessionMovesOn(t *testing.T) {
	m := &Model{}
	sess := store.Session{ID: "a1", Name: "ask", Status: status.Waiting, LastStatusAt: time.Now()}

	unansweredKeys(m, sess, deafKeyRun)
	if !m.isDeaf(sess) {
		t.Fatalf("the pane was never marked")
	}

	revived := sess
	revived.Status = status.Idle
	revived.LastStatusAt = sess.LastStatusAt.Add(time.Second)
	if m.isDeaf(revived) {
		t.Fatalf("the mark outlived the state it was made in")
	}
	if m.isDeaf(sess) {
		t.Fatalf("the lapsed mark was left in the map")
	}
}

// The point of the mark: a wedged pane reads "waiting" for as long as it is
// wedged, so without this the drain hands it back on every pass and the
// operator answers a session that cannot hear them.
func TestTriageWalksPastADeafSession(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"first":  status.Waiting,
		"second": status.Waiting,
		"third":  status.Waiting,
	})
	m.triage = true
	m.rebuildRows()
	m.markDeaf(sessionNamed(t, m, "second"))
	m.enterFocusOn(t, "first")

	var seen []string
	for range 3 {
		var name string
		if m, name = ctrlQWalk(t, m); name == "" {
			break
		}
		seen = append(seen, name)
	}
	for _, name := range seen {
		if name == "second" {
			t.Fatalf("the drain handed over the deaf session: walk = %v", seen)
		}
	}
	if len(seen) != 1 || seen[0] != "third" {
		t.Fatalf("ctrl+q walk = %v, want third alone", seen)
	}
}

// The chase measures the pane against a baseline taken immediately before the
// key, and focusEchoCmd's own doc says the frame on screen is not the same
// thing -- a tick may have replaced it since. So a chase that DID see the pane
// echo, returning a frame the screen already happens to hold, must not be read
// as a key that came to nothing. Recomputing the comparison here instead of
// carrying the chase's verdict marked a live pane deaf after six such keys.
func TestAChaseThatEchoedIsNeverCountedAgainstThePane(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess := sessionNamed(t, m, "ask")

	m.preview = "the frame the chase brings back"
	for range deafKeyRun {
		updated, _ := m.update(previewMsg{
			sessID:  sess.ID,
			gen:     m.previewGen,
			at:      time.Now(),
			chase:   true,
			echoed:  true,
			preview: m.preview,
		})
		m = updated.(*Model)
	}

	if m.isDeaf(sess) {
		t.Fatalf("a pane that echoed every key was marked deaf")
	}
}
