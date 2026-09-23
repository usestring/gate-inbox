package ui

import (
	"slices"
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
)

// The default is unchanged: with no queue armed and the setting untouched,
// ctrl+q returns to the board and stops there.
func TestLeavingReturnsToTheListByDefault(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
	})
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("ctrl+q carried on into %q with the setting at its default", focusedName(t, m))
	}
}

// With the setting on, the same key walks the queue without triage having
// been armed first -- which is the whole complaint: the walk existed and
// nothing said so.
func TestLeavingAdvancesWithTheSettingOnAndNoTriage(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"busy":  status.Working,
	})
	m.leaveMode = leaveToNext
	m.rebuildRows()
	if m.triage {
		t.Fatal("the fixture armed triage; this test is about the setting alone")
	}

	m.enterFocusOn(t, "ask")
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("ctrl+q dropped to the list instead of advancing: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "broke" {
		t.Fatalf("ctrl+q landed on %q, want the next session needing a person (broke)", got)
	}

	// And it converges: a walk that handed the same session back would never
	// end, so the second hop must run out rather than return to "ask".
	updated, _ = m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode == modeFocus {
		t.Fatalf("the walk came round again to %q instead of draining", focusedName(t, m))
	}
}

// A working session is not somebody's turn to take, so the walk must not
// hand one over even when it is the only row left.
func TestLeavingNeverAdvancesIntoAWorkingSession(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"busy": status.Working,
	})
	m.leaveMode = leaveToNext
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode == modeFocus {
		t.Fatalf("the walk entered %q, which is mid-turn", focusedName(t, m))
	}
}

// ctrl+\ is the way out whatever the setting says. Without this the setting
// would take away the only key that reliably returns to the board.
func TestTheSecondExitStillLeavesWithTheSettingOn(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
	})
	m.leaveMode = leaveToNext
	m.rebuildRows()

	m.enterFocusOn(t, "ask")
	updated, _ := m.handleFocusKey(ctrlBackslash())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("ctrl+backslash advanced into %q instead of leaving", focusedName(t, m))
	}
}

// Triage is a queue armed on purpose and walks it whatever the setting says,
// so turning the setting off cannot break the drain.
func TestTriageStillWalksWithTheSettingOff(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
	})
	m.triage, m.leaveMode = true, leaveToList
	m.rebuildRows()
	if got, want := sessionNames(m), []string{"ask", "broke"}; !slices.Equal(got, want) {
		t.Fatalf("queue = %v want %v", got, want)
	}

	m.enterFocusOn(t, "ask")
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("triage stopped walking when the setting was off: %s", m.errBar.text)
	}
	if got := focusedName(t, m); got != "broke" {
		t.Errorf("triage landed on %q, want broke", got)
	}
}

// An advance nothing announces reads as the manager losing your place, and
// the focused footer is the only surface left to announce it on.
func TestTheFocusedFooterNamesTheAdvance(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	m.enterFocusOn(t, "ask")

	if hint := m.leaveAdvanceHint(); hint == "" {
		t.Fatal("the advance has no hint to put on the footer")
	}
	if m.advancesOnLeave() {
		t.Fatal("the default advertises an advance it does not do")
	}
	m.leaveMode = leaveToNext
	if !m.advancesOnLeave() {
		t.Error("the setting did not turn the advance on")
	}
	// Triage names the group it is scoped to; the setting has none to name,
	// so it must not borrow triage's wording.
	m.triageScope = "team"
	if got := m.leaveAdvanceHint(); got != "mute this, next needing input" {
		t.Errorf("outside triage the hint reads %q; it must not name a triage scope", got)
	}
}

// The choice survives the panel and the store, and an unknown stored value
// leaves ctrl+q doing what it has always done.
func TestLeaveModeRoundTripsAndFailsSafe(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.settings.field = settingsFieldLeave
	m.applyCmd(t, m.cycleSetting(1))
	if _, cmd := m.saveAndCloseSettings(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.leaveMode != leaveToNext {
		t.Fatalf("model leaveMode %q, want %q", m.leaveMode, leaveToNext)
	}
	if got := storedLeaveMode(m.store); got != leaveToNext {
		t.Fatalf("stored %q, want %q", got, leaveToNext)
	}

	if err := m.store.SetSetting(leaveSetting, "onwards"); err != nil {
		t.Fatal(err)
	}
	if got := storedLeaveMode(m.store); got != leaveToList {
		t.Errorf("an unknown stored value read as %q, want %q", got, leaveToList)
	}
}
