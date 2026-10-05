package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/store"
)

// The third choice is offered only where its setting can take effect: one
// thing the operator picked out. A group teardown and the ticked wide sweep
// keep their two answers.
func TestConfirmAlwaysIsOfferedOnlyOnAPickedOutAct(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("team", dir); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	createSession(t, m, "alpha", dir, "team")
	createSession(t, m, "beta", dir, "team")

	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !m.confirmSilenceable() {
		t.Fatal("a kill of one picked-out session should offer don't ask again")
	}
	if !strings.Contains(cardText(m), "don't ask again") {
		t.Fatalf("the dialog does not offer don't ask again:\n%s", cardText(m))
	}
	m.mode, m.confirm = modeList, confirmTarget{}

	m.selectGroupRow(t, "team")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.confirmSilenceable() {
		t.Fatal("a group kill should keep both answers")
	}
	if strings.Contains(cardText(m), "don't ask again") {
		t.Fatalf("the group dialog offers don't ask again:\n%s", cardText(m))
	}
	m.mode, m.confirm = modeList, confirmTarget{}

	if _, cmd := m.archiveAllLive(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.confirmSilenceable() {
		t.Fatal("the ticked sweep should keep both answers")
	}
}

// A writes the action's own setting and answers the dialog in the same
// keystroke, so the next kill of one session never asks again.
func TestConfirmAlwaysKillsAndStopsAsking(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeConfirmDelete {
		t.Fatalf("no dialog to answer; mode is %v", m.mode)
	}
	if _, cmd := m.handleConfirmKey(key("A")); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !archived(t, m, "alpha") {
		t.Fatalf("A did not archive alpha; bar says %q", m.errBar.text)
	}
	if got := storedArchiveConfirm(m.store); got != archiveConfirmNever {
		t.Fatalf("stored archive_confirm = %q, want %q", got, archiveConfirmNever)
	}

	createSession(t, m, "beta", t.TempDir(), "")
	m.selectSessionRow(t, "beta")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode == modeConfirmDelete {
		t.Fatal("the next single-session kill still asked")
	}
	if !archived(t, m, "beta") {
		t.Fatalf("beta was not archived; bar says %q", m.errBar.text)
	}
}

// The restart setting is its own: silencing the kill dialog must not silence
// the restart one, and A on a restart writes only restart_confirm.
func TestConfirmAlwaysRestartsAndStopsAsking(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.restartSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeConfirmDelete || m.confirm.action != actionRestart {
		t.Fatalf("no restart dialog; mode %v action %q", m.mode, m.confirm.action)
	}
	if _, cmd := m.handleConfirmKey(key("A")); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := storedConfirmAsk(m.store, restartConfirmSetting); got != confirmNever {
		t.Fatalf("stored restart_confirm = %q, want %q", got, confirmNever)
	}
	if got := storedArchiveConfirm(m.store); got != archiveConfirmAlways {
		t.Fatalf("A on a restart changed archive_confirm to %q", got)
	}

	if _, cmd := m.restartSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode == modeConfirmDelete {
		t.Fatal("the next restart still asked")
	}
}

// Deleting an empty group is the one destructive act with nothing running
// under it, so it too offers the choice, and A takes the group and stops
// asking.
func TestConfirmAlwaysDeletesAnEmptyGroupAndStopsAsking(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("team", dir); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.selectGroupRow(t, "team")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeConfirmDelete || m.confirm.action != actionDelete {
		t.Fatalf("no delete dialog; mode %v action %q", m.mode, m.confirm.action)
	}
	if _, cmd := m.handleConfirmKey(key("A")); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if got := storedConfirmAsk(m.store, deleteConfirmSetting); got != confirmNever {
		t.Fatalf("stored delete_confirm = %q, want %q", got, confirmNever)
	}
	if !strings.Contains(m.errBar.text, "deleted") {
		t.Fatalf("the group was not deleted; bar says %q", m.errBar.text)
	}

	if err := m.store.CreateGroup("team2", dir); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.selectGroupRow(t, "team2")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode == modeConfirmDelete {
		t.Fatal("the next empty-group delete still asked")
	}
	if !strings.Contains(m.errBar.text, "deleted") {
		t.Fatalf("team2 was not deleted; bar says %q", m.errBar.text)
	}
}

// A on a ticked wide sweep is not an answer: the setting never silences it,
// so the key is ignored and the dialog stands.
func TestConfirmAlwaysKeyIsIgnoredOnAWideSweep(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	createSession(t, m, "beta", t.TempDir(), "")
	if _, cmd := m.archiveAllLive(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeConfirmDelete || m.confirm.ack == "" {
		t.Fatalf("the sweep did not raise its ticked dialog; mode %v", m.mode)
	}
	if _, cmd := m.handleConfirmKey(key("A")); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeConfirmDelete {
		t.Fatal("A answered the wide sweep")
	}
	if archived(t, m, "alpha") || archived(t, m, "beta") {
		t.Fatal("A archived a session on a wide sweep")
	}
}

// The two new settings round-trip through the panel and the store, and an
// unknown value reads as asking rather than as skipping.
func TestDeleteAndRestartConfirmRoundTripAndFailSafe(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	for _, tc := range []struct {
		field   int
		setting string
	}{
		{settingsFieldDeleteConfirm, deleteConfirmSetting},
		{settingsFieldRestartConfirm, restartConfirmSetting},
	} {
		m.settings.field = tc.field
		m.applyCmd(t, m.cycleSetting(1))
		if _, cmd := m.saveAndCloseSettings(); cmd != nil {
			m.applyCmd(t, cmd)
		}
		if got := storedConfirmAsk(m.store, tc.setting); got != confirmNever {
			t.Fatalf("%s stored %q, want %q", tc.setting, got, confirmNever)
		}
		if err := m.store.SetSetting(tc.setting, "sometimes"); err != nil {
			t.Fatal(err)
		}
		if got := storedConfirmAsk(m.store, tc.setting); got != confirmAlways {
			t.Fatalf("%s read an unknown value as %q, want %q", tc.setting, got, confirmAlways)
		}
	}
}

// A stray A on a dialog that has no setting is not an answer either: restore
// keeps its two keys.
func TestConfirmAlwaysIsIgnoredWhereThereIsNoSetting(t *testing.T) {
	m := buildModel(t)
	m.mode = modeConfirmDelete
	m.confirm = confirmTarget{
		action:   actionRestore,
		sessions: []store.Session{{ID: "one", Name: "alpha"}},
	}
	if _, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'A', Text: "A"}); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode != modeConfirmDelete {
		t.Fatal("A answered a restore, which has no don't-ask-again")
	}
}
