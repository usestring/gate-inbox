package ui

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
)

// The mute key stores a flag on the session and the row wears it as its
// state: "muted" in place of the pane status, kept on the list rather than
// dropped from it.
func TestMuteKeyStoresTheFlagAndTheRowReadsMuted(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "busy": status.Working})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	updated, _ := m.handleKey(key("M"))
	m = updated.(*Model)
	ask := sessionNamed(t, m, "ask")
	if !ask.Muted {
		t.Fatalf("M did not mute: %s", m.errBar.text)
	}
	stored, err := m.store.Get(ask.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !stored.Muted {
		t.Fatal("the mute never reached the store")
	}
	rail := triageRail(m)
	if !strings.Contains(rail, mutedStatusLabel) {
		t.Fatalf("a muted row does not read %q:\n%s", mutedStatusLabel, rail)
	}
	if strings.Contains(rail, "waiting") {
		t.Fatalf("a muted row still reads its pane status:\n%s", rail)
	}
	if m.needsPerson(ask) || m.triageWalkable(ask) {
		t.Fatal("a muted session still counts as on the operator's queue")
	}

	// The same key puts it back.
	updated, _ = m.handleKey(key("M"))
	m = updated.(*Model)
	if sessionNamed(t, m, "ask").Muted {
		t.Fatal("M did not unmute")
	}
	stored, err = m.store.Get(sessionNamed(t, m, "ask").ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Muted {
		t.Fatal("unmuting never reached the store")
	}
}

// A persistently muted session never reappears in triage, on the rail or in the
// drain, until it is explicitly unmuted.
func TestMutedSessionStaysOutOfTriageUntilUnmuted(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "other": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	updated, _ := m.handleKey(key("M"))
	m = updated.(*Model)

	m.triage = true
	m.rebuildRows()
	if rail := triageRail(m); strings.Contains(rail, "ask") {
		t.Fatalf("a muted session reappeared in triage:\n%s", rail)
	}
	if names := sessionNames(m); len(names) != 1 || names[0] != "other" {
		t.Fatalf("triage queue = %v, want only other", names)
	}
	if cmd := m.enterTriageHead(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := focusedName(t, m); got != "other" {
		t.Fatalf("the drain handed over %q, want other", got)
	}

	// Unmuting from the list brings the row back to the queue.
	m.mode = modeList
	m.triage = false
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	updated, _ = m.handleKey(key("M"))
	m = updated.(*Model)
	m.triage = true
	m.rebuildRows()
	if rail := triageRail(m); !strings.Contains(rail, "ask") {
		t.Fatalf("an unmuted session did not return to triage:\n%s", rail)
	}
}

// The skip key on a muted row is the way back: it un-mutes rather than
// refusing the row as one that is not waiting on anybody.
func TestSkipKeyUnmutesADurablyMutedRow(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")
	updated, _ := m.handleKey(key("M"))
	m = updated.(*Model)

	updated, _ = m.handleKey(key("."))
	m = updated.(*Model)
	if sessionNamed(t, m, "ask").Muted {
		t.Fatalf(`"." did not unmute the row: %s`, m.errBar.text)
	}
}
