package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
)

func TestAutomaticHibernateDefaultsOnAndHonorsSavedOff(t *testing.T) {
	m := buildModel(t)
	m.cfg.HibernateAfter.Duration = 0
	m.snap = sysstat.Snapshot{MemOK: true, MemTotal: 16 << 30, MemPercent: 50}
	if got := m.hibernateSensitivity(); got != "balanced" {
		t.Fatalf("default sensitivity = %q", got)
	}
	if cmd := m.settleHibernated(); cmd == nil || m.hibernateSweptAt.IsZero() {
		t.Fatal("default policy did not schedule automatic hibernation")
	}
	m.hibernating = false
	m.hibernateSweptAt = time.Time{}
	m.openSettings()
	m.settings.hibernateSensitivity = "off"
	m.persistSettings()
	m.openSettings()
	if m.settings.hibernateSensitivity != "off" {
		t.Fatal("saved opt-out did not survive reopening settings")
	}
	m.settleHibernated()
	if !m.hibernateSweptAt.IsZero() {
		t.Fatal("disabled policy still scheduled a sweep")
	}
}

func TestHibernateSweepUsesCachedPressureCrossingsAndTenMinuteBackstop(t *testing.T) {
	m := &Model{
		cfg:  config.Config{Tools: map[string]config.Tool{"terminal": {Shell: true}}},
		snap: sysstat.Snapshot{MemOK: true, MemTotal: 4 << 30, MemPercent: 50},
		sessions: []store.Session{
			{Tool: "claude", Status: status.Working},
			{Tool: "claude", Status: status.Idle},
			{Tool: "claude", Status: status.Dead},
			{Tool: "claude", Status: status.Idle, Archived: true},
			{Tool: "terminal", Status: status.Idle},
		},
	}
	start := time.Unix(1_700_000_000, 0)
	if !m.hibernateSweepDue("balanced", start) {
		t.Fatal("startup did not run the backstop sweep")
	}
	if m.hibernateSweepDue("balanced", start.Add(time.Minute)) {
		t.Fatal("healthy board repeated the full sweep after only a minute")
	}
	m.sessions = append(m.sessions, store.Session{Tool: "claude", Status: status.Starting})
	if !m.hibernateSweepDue("balanced", start.Add(2*time.Minute)) {
		t.Fatal("crossing the session budget did not trigger a sweep")
	}
	if m.hibernateSweepDue("balanced", start.Add(3*time.Minute)) {
		t.Fatal("sustained pressure repeated the full sweep after only a minute")
	}
	if m.hibernateSweepDue("balanced", start.Add(12*time.Minute-time.Nanosecond)) {
		t.Fatal("backstop ran before ten minutes elapsed")
	}
	if !m.hibernateSweepDue("balanced", start.Add(12*time.Minute)) {
		t.Fatal("sustained pressure did not retry after ten minutes")
	}
	m.sessions = m.sessions[:2]
	if m.hibernateSweepDue("balanced", start.Add(13*time.Minute)) {
		t.Fatal("recovering below the budget triggered a sweep")
	}
	m.snap.MemPercent = 80
	if !m.hibernateSweepDue("balanced", start.Add(14*time.Minute)) {
		t.Fatal("crossing the memory threshold below the session budget did not trigger a sweep")
	}
	m.snap.MemPercent = 50
	m.hibernateSweepDue("balanced", start.Add(15*time.Minute))
	m.snap.MemPercent = 80
	if !m.hibernateSweepDue("balanced", start.Add(16*time.Minute)) {
		t.Fatal("a new pressure crossing after recovery did not trigger a sweep")
	}
	m.snap.MemPercent = 50
	m.hibernateSweepDue("balanced", start.Add(17*time.Minute))
	if !m.hibernateSweepDue("balanced", start.Add(26*time.Minute)) {
		t.Fatal("healthy board did not run the ten-minute backstop")
	}
}

func TestHibernateSweepRequiresEnabledPolicyAndMemoryReading(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sensitivity string
		memory      sysstat.Snapshot
	}{
		{"disabled", "off", sysstat.Snapshot{MemOK: true, MemTotal: 1 << 30, MemPercent: 99}},
		{"missing reading", "balanced", sysstat.Snapshot{}},
		{"missing total", "balanced", sysstat.Snapshot{MemOK: true, MemPercent: 99}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{snap: tc.memory, sessions: []store.Session{{Status: status.Idle}}}
			if m.hibernateSweepDue(tc.sensitivity, time.Unix(1_700_000_000, 0)) || !m.hibernateSweptAt.IsZero() {
				t.Fatal("disabled or unreadable policy scheduled a sweep")
			}
		})
	}
}

// hibernatedRow is a board session whose pane hibernate stopped: the row
// dead in the store and in m.sessions, its id on the hibernated set.
func hibernatedRow(t *testing.T, m *Model, name string) store.Session {
	t.Helper()
	createSession(t, m, name, t.TempDir(), "")
	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := m.store.UpdateStatus(sess.ID, status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if err := m.store.AddHibernated([]string{sess.ID}); err != nil {
		t.Fatalf("AddHibernated: %v", err)
	}
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].Status = status.Dead
		}
	}
	m.hibernated = []string{sess.ID}
	m.rebuildRows()
	return m.sessionRows()[0]
}

// Focusing a hibernated session is asking for it back. It wakes on its
// conversation and focus lands on its pane, rather than on the dead-row
// hint that sent people to revive it by hand.
func TestFocusingAHibernatedSessionWakesIt(t *testing.T) {
	m := buildModel(t)
	sess := hibernatedRow(t, m, "sleeper")

	m.focusSelected()

	if !m.tmux.Exists(sess.ID) {
		t.Fatalf("focus did not wake the hibernated session; err bar %q", m.errBar.text)
	}
	if m.mode != modeFocus {
		t.Fatalf("mode = %v, want focus on the woken pane", m.mode)
	}
	if still, _ := m.store.IsHibernated(sess.ID); still {
		t.Fatal("the woken session is still marked hibernated")
	}
	if got := m.shownStatus(m.sessionRows()[0]); got == status.Dead {
		t.Fatal("the woken row reads dead until the next refresh")
	}
}

// Attaching wakes it the same way.
func TestAttachingAHibernatedSessionWakesIt(t *testing.T) {
	m := buildModel(t)
	sess := hibernatedRow(t, m, "sleeper")

	_, cmd := m.attachSelected()

	if !m.tmux.Exists(sess.ID) || cmd == nil {
		t.Fatalf("attach did not wake and attach the hibernated session (cmd %v); err bar %q", cmd != nil, m.errBar.text)
	}
}

// A row that really died is not woken by focus: it still gets the hint.
func TestFocusingADeadSessionStillGetsTheHint(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "gone", t.TempDir(), "")
	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}

	m.focusSelected()

	if m.tmux.Exists(sess.ID) {
		t.Fatal("focus revived a session hibernate never stopped")
	}
	if m.errBar.text != m.deadSessionHint() {
		t.Fatalf("err bar = %q, want the dead-row hint", m.errBar.text)
	}
}

// The board draws a hibernated row as hibernating, never dead, and triage
// ranks it below everything that could use the operator. A dead row beside
// it keeps its dead mark.
func TestAHibernatedRowShowsHibernatingAndTriageDemotesIt(t *testing.T) {
	m := &Model{hibernated: []string{"sleep001"}}
	sleeping := store.Session{ID: "sleep001", Status: status.Dead}
	gone := store.Session{ID: "gone0001", Status: status.Dead}

	if got := m.shownStatus(sleeping); got != status.Hibernating {
		t.Fatalf("shownStatus = %s, want hibernating", got)
	}
	if got := m.shownStatus(gone); got != status.Dead {
		t.Fatalf("shownStatus of a dead row = %s, want dead", got)
	}
	if statusGlyph(status.Hibernating) == statusGlyph(status.Dead) {
		t.Fatal("a hibernated row wears the dead mark")
	}
	if got := m.triageStateOf(sleeping); got != status.Hibernating {
		t.Fatalf("triage state = %s, want hibernating", got)
	}
	if requiresInput(m.triageStateOf(sleeping)) {
		t.Fatal("a hibernated row counts as needing the operator")
	}
	if triageRank(status.Hibernating) <= triageRank(status.Starting) {
		t.Fatal("triage ranks a hibernated row above a starting one")
	}
}

// Wake-all brings back every hibernated row in view, one relaunch per
// step, ignores a second press while its run is going, and leaves a row
// that really died for revive.
func TestWakeAllWakesEveryHibernatedSessionAndNoDeadOne(t *testing.T) {
	m := buildModel(t)
	byName := map[string]store.Session{}
	for _, name := range []string{"gone", "sleeper-one", "sleeper-two"} {
		createSession(t, m, name, t.TempDir(), "")
	}
	for _, sess := range m.sessionRows() {
		byName[sess.Name] = sess
		if err := m.tmux.Kill(sess.ID); err != nil {
			t.Fatalf("kill: %v", err)
		}
		if err := m.store.UpdateStatus(sess.ID, status.Dead); err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
	}
	dead, first, second := byName["gone"], byName["sleeper-one"], byName["sleeper-two"]
	m.hibernated = []string{first.ID, second.ID}
	if err := m.store.AddHibernated(m.hibernated); err != nil {
		t.Fatalf("AddHibernated: %v", err)
	}
	for i := range m.sessions {
		m.sessions[i].Status = status.Dead
	}
	m.rebuildRows()

	if !m.applies(keymap.ContextList, keymap.WakeAll, treeRow{}) {
		t.Fatal("wake-all does not apply with hibernated rows listed")
	}
	_, cmd := m.runListAction(keymap.WakeAll, tea.KeyPressMsg{})
	if cmd == nil {
		t.Fatalf("wake-all scheduled no next step; err bar %q", m.errBar.text)
	}
	if !m.tmux.Exists(first.ID) || m.tmux.Exists(second.ID) {
		t.Fatalf("first step should wake only %s", first.Name)
	}
	if _, again := m.runListAction(keymap.WakeAll, tea.KeyPressMsg{}); again != nil {
		t.Fatal("a second press started another wake-all run")
	}
	if !strings.Contains(m.errBar.text, "already waking") {
		t.Fatalf("err bar on a second press = %q", m.errBar.text)
	}
	for steps := 0; m.wakeAll != nil; steps++ {
		if steps > 5 {
			t.Fatal("wake-all run never finished")
		}
		m.wakeAllStep()
	}

	for _, sess := range []store.Session{first, second} {
		if !m.tmux.Exists(sess.ID) {
			t.Fatalf("wake-all did not wake %s; err bar %q", sess.Name, m.errBar.text)
		}
		if still, _ := m.store.IsHibernated(sess.ID); still {
			t.Fatalf("%s is still marked hibernated", sess.Name)
		}
	}
	if m.tmux.Exists(dead.ID) {
		t.Fatal("wake-all revived a session hibernate never stopped")
	}
	if len(m.hibernated) != 0 {
		t.Fatalf("m.hibernated = %v, want empty", m.hibernated)
	}
	if m.applies(keymap.ContextList, keymap.WakeAll, treeRow{}) {
		t.Fatal("wake-all still applies with nothing hibernated")
	}
}
