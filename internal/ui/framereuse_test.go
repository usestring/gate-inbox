package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

func frameRows(s string) int { return len(strings.Split(s, "\n")) }

// moveTheFleet changes a session the rail is certain to paint, so a frame
// taken after it differs from one taken before. The fixture's rail scrolls,
// so its first session sits above the viewport and nothing on screen reads
// its status: a change there leaves the frame byte-identical, which would
// let every comparison below pass on a cache that never worked. The cursor's
// session is the one row the rail always has room for.
func moveTheFleet(t *testing.T, m *Model) {
	t.Helper()
	sess, ok := m.selected()
	if !ok {
		t.Fatal("the fixture has no selected session to move")
	}
	for i := range m.sessions {
		if m.sessions[i].ID != sess.ID {
			continue
		}
		if m.sessions[i].Status == status.Errored {
			m.sessions[i].Status = status.Working
		} else {
			m.sessions[i].Status = status.Errored
		}
		m.rebuildRows()
		return
	}
	t.Fatalf("selected session %s is not in the fleet", sess.ID)
}

// TestFrameReuseServesTheLastFrame proves the flag is doing something: the
// model is moved under a marked frame, and what comes back is the frame from
// before the move.
func TestFrameReuseServesTheLastFrame(t *testing.T) {
	m := fleetModel(t, 12, 120, 34)
	before := m.frame()

	moveTheFleet(t, m)
	m.frameUnchanged()
	if got := m.frame(); got != before {
		t.Fatal("a marked frame was re-rendered instead of served from the last one")
	}
	if got := m.frame(); got == before {
		t.Fatal("the frame after it stayed stale")
	}
}

// TestFrameReuseIsOneShot: the mark covers the one paint that follows the
// message, not every paint after it.
func TestFrameReuseIsOneShot(t *testing.T) {
	m := fleetModel(t, 12, 120, 34)
	_ = m.frame()
	m.frameUnchanged()
	_ = m.frame()
	if m.frameReuse {
		t.Fatal("the mark outlived its frame")
	}
}

func TestFrameReuseExpires(t *testing.T) {
	m := fleetModel(t, 12, 120, 34)
	before := m.frame()

	moveTheFleet(t, m)
	m.lastFrameAt = time.Now().Add(-frameReuseWindow - time.Millisecond)
	m.frameUnchanged()
	if got := m.frame(); got == before {
		t.Fatal("a frame past the reuse window was served anyway")
	}
}

// TestFrameReuseRefusesAResize: a frame is the size it was painted at, so a
// terminal that moved gets a new one however little else changed.
func TestFrameReuseRefusesAResize(t *testing.T) {
	m := fleetModel(t, 12, 120, 34)
	before := m.frame()

	m.width, m.height = 100, 30
	m.frameUnchanged()
	got := m.frame()
	if got == before {
		t.Fatal("a resized frame was served from the old geometry")
	}
	if frameRows(got) != 30 {
		t.Fatalf("frame is %d rows, want 30", frameRows(got))
	}
}

// TestFrameSessionSetsAreRebuiltEachFrame guards the arrays visibleSessions
// and listedAgents refill rather than reallocate while painting. Reuse is
// only sound because a frame rebuilds both before anything reads them, and
// the failure it would hide is a quiet one: only the rollups read those sets,
// so a stale array leaves the tree right and the counts above it wrong.
//
// So the test does not ask whether the frame changed -- the tree would change
// it either way. It paints a model that has been moved and compares it to a
// model that was built that way, which is the only comparison a stale count
// cannot pass.
func TestFrameSessionSetsAreRebuiltEachFrame(t *testing.T) {
	moved := fleetModel(t, 12, 120, 34)
	_ = moved.frame()
	moved.sessions = fleetSessions(8)
	moved.rebuildRows()

	fresh := fleetModel(t, 8, 120, 34)
	if got, want := moved.frame(), fresh.frame(); got != want {
		t.Fatal("a frame painted after the fleet shrank did not match one painted by a model that always had it")
	}
}
