package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// answerDialog says yes to the dialog on screen, ticking its box first when
// it has one.
func answerDialog(t testing.TB, m *Model) {
	t.Helper()
	if m.mode != modeConfirmDelete {
		t.Fatalf("no confirmation on screen; mode is %v", m.mode)
	}
	if m.confirm.ack != "" {
		m.handleConfirmKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.applyCmd(t, cmd)
}

// archived reports whether a named session is filed away, read back off the
// store rather than off the board: the board hides archived rows, so a row
// that has merely scrolled out of the tree would read the same as one that
// was archived.
func archived(t testing.TB, m *Model, name string) bool {
	t.Helper()
	sessions, err := m.store.ListSessions(true)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	for _, sess := range sessions {
		if sess.Name == name {
			return sess.Archived
		}
	}
	t.Fatalf("no session named %q in the store", name)
	return false
}

// The default is unchanged: x still asks, so nobody's board starts behaving
// differently on an upgrade.
func TestArchiveAsksByDefault(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeConfirmDelete {
		t.Fatalf("x did not raise the dialog; mode is %v", m.mode)
	}
	if archived(t, m, "alpha") {
		t.Error("the session was archived before the dialog was answered")
	}
}

// With the setting off, x on one session files it away on the keystroke, and
// says so -- naming the way back, which is what the dialog it replaced used
// to do.
func TestArchiveWithoutAskingFilesItAndNamesTheWayBack(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.archiveConfirm = archiveConfirmNever
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode == modeConfirmDelete {
		t.Fatal("x still raised the dialog with the setting off")
	}
	if !archived(t, m, "alpha") {
		t.Fatalf("alpha was not archived; bar says %q", m.errBar.text)
	}
	if !m.errBar.worked() {
		t.Errorf("the notice read as a failure: %q", m.errBar.text)
	}
	if !strings.Contains(m.errBar.text, "U") {
		t.Errorf("notice %q does not name the key that takes it back", m.errBar.text)
	}
}

// The wide gestures keep their dialog whatever the setting says. A silent
// answer is safe because the operator picked the session out; x on a group
// and X on the whole view are not that.
func TestTheWideArchiveGesturesStillAsk(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("team", dir); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	createSession(t, m, "alpha", dir, "team")
	createSession(t, m, "beta", dir, "team")
	m.archiveConfirm = archiveConfirmNever

	m.selectGroupRow(t, "team")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeConfirmDelete {
		t.Fatalf("x on a group skipped the dialog; mode is %v", m.mode)
	}
	m.mode, m.confirm = modeList, confirmTarget{}

	if _, cmd := m.archiveAllLive(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeConfirmDelete {
		t.Fatalf("X skipped the dialog; mode is %v", m.mode)
	}
	if m.confirm.ack == "" {
		t.Error("X lost its tick")
	}
}

// U is the undo the setting is paid for with: the session comes back out of
// the archive and back onto the board.
func TestUndoBringsTheLastArchiveBack(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.archiveConfirm = archiveConfirmNever
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !archived(t, m, "alpha") {
		t.Fatalf("alpha was not archived; bar says %q", m.errBar.text)
	}

	if _, cmd := m.undoArchive(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if archived(t, m, "alpha") {
		t.Fatalf("U left alpha archived; bar says %q", m.errBar.text)
	}
	if !m.errBar.worked() {
		t.Errorf("the undo notice read as a failure: %q", m.errBar.text)
	}
}

// A confirmed archive is offered back too. The dialog said what was about to
// happen, not that it was meant, and a mis-keyed y is as worth taking back
// as a mis-keyed x.
func TestAConfirmedArchiveIsAlsoUndoable(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	answerDialog(t, m)
	if !archived(t, m, "alpha") {
		t.Fatalf("alpha was not archived; bar says %q", m.errBar.text)
	}

	if _, cmd := m.undoArchive(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if archived(t, m, "alpha") {
		t.Errorf("U left a confirmed archive in place; bar says %q", m.errBar.text)
	}
}

// The undo is spent once, so a second U cannot re-run a restore that has
// already happened or re-report one that failed.
func TestUndoIsSpentOnce(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.archiveConfirm = archiveConfirmNever
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if _, cmd := m.undoArchive(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if _, cmd := m.undoArchive(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !strings.Contains(m.errBar.text, "nothing archived") {
		t.Errorf("a second U said %q; it should report there is nothing left to undo", m.errBar.text)
	}
}

// With nothing archived yet, U refuses rather than acting on whatever the
// cursor is on -- which is u's job, not this key's.
func TestUndoRefusesWithNothingArchived(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.undoArchive(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if archived(t, m, "alpha") {
		t.Fatal("U archived the row under the cursor")
	}
	if !strings.Contains(m.errBar.text, "nothing archived") {
		t.Errorf("bar said %q, want a report that there is nothing to undo", m.errBar.text)
	}
}

// The footer names U only once there is something to take back.
func TestTheFooterNamesUndoOnlyAfterAnArchive(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	createSession(t, m, "beta", dir, "")
	m.archiveConfirm = archiveConfirmNever
	m.selectSessionRow(t, "beta")
	if legendPair(m.rowLegend(), "U") {
		t.Error("the footer named U with nothing archived")
	}
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	m.selectSessionRow(t, "beta")
	if !legendPair(m.rowLegend(), "U") {
		t.Error("the footer did not name U after an archive")
	}
}

func legendPair(section legendSection, key string) bool {
	for _, pair := range section.pairs {
		if pair[0] == key {
			return true
		}
	}
	return false
}

// The choice survives the settings panel and the store, and a value this
// build does not know reads as the safe end of the setting rather than as
// no confirmation at all.
func TestArchiveConfirmRoundTripsAndFailsSafe(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.settings.field = settingsFieldArchiveConfirm
	m.applyCmd(t, m.cycleSetting(1))
	if _, cmd := m.saveAndCloseSettings(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.archiveConfirm != archiveConfirmNever {
		t.Fatalf("model archiveConfirm %q, want %q", m.archiveConfirm, archiveConfirmNever)
	}
	if got := storedArchiveConfirm(m.store); got != archiveConfirmNever {
		t.Fatalf("stored %q, want %q", got, archiveConfirmNever)
	}

	if err := m.store.SetSetting(archiveConfirmSetting, "sometimes"); err != nil {
		t.Fatal(err)
	}
	if got := storedArchiveConfirm(m.store); got != archiveConfirmAlways {
		t.Errorf("an unknown stored value read as %q; it must fail safe to %q", got, archiveConfirmAlways)
	}
}
