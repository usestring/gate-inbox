package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// restoreModel is a manager holding the given rows, with one tool that can
// resume by id, which is what separates an exact resume from a degraded one.
func restoreModel(sessions ...store.Session) *Model {
	return &Model{
		mode:         modeList,
		width:        120,
		height:       40,
		sessions:     sessions,
		restoreArmed: true,
		cfg: config.Config{Tools: map[string]config.Tool{
			"claude": {ResumeByIDCommand: "claude --resume {id}"},
		}},
	}
}

func deadSession(id, name, agentID string) store.Session {
	return store.Session{ID: id, Name: name, Tool: "claude", Cwd: "/repo",
		Status: status.Dead, AgentSessionID: agentID,
		AgentLaunchedAt: launchedAt, LastStatusAt: launchedAt.Add(time.Hour)}
}

// launchedAt stands for the start of the agent that was lost. The ledger keys
// on it, so a second launch only stays quiet when it reads the same instant
// back.
var launchedAt = time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC)

// restoreStore is a real store for the launch-to-launch tests: the whole
// point of the ledger is that it survives the Model that wrote it.
func restoreStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// relaunch is the next run of the manager over the same store and rows.
func relaunch(st *store.Store, sessions ...store.Session) *Model {
	m := restoreModel(sessions...)
	m.store = st
	return m
}

func TestRestoreOffersOnlyDeadUnarchivedRows(t *testing.T) {
	m := restoreModel(
		deadSession("a", "alpha", "id-a"),
		store.Session{ID: "b", Name: "beta", Tool: "claude", Status: status.Working},
		store.Session{ID: "c", Name: "gamma", Tool: "claude", Status: status.Dead, Archived: true},
	)
	got := m.restoreCandidates()
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("expected only the dead unarchived row, got %+v", got)
	}
}

func TestStartupPromptOpensForMissingPanesAndOnlyOnce(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"), deadSession("b", "beta", "id-b"))
	m.maybeOpenRestorePrompt()
	if m.mode != modeRestorePrompt {
		t.Fatalf("expected the prompt to open, mode=%v", m.mode)
	}
	if len(m.restore.candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(m.restore.candidates))
	}
	// Dismissing must be final: a fleet the operator declined cannot come
	// back on the next refresh.
	m.closeRestorePrompt()
	m.maybeOpenRestorePrompt()
	if m.mode != modeList {
		t.Fatalf("prompt re-opened after dismissal, mode=%v", m.mode)
	}
}

func TestStartupPromptStaysQuietWhenNothingIsDead(t *testing.T) {
	m := restoreModel(store.Session{ID: "a", Tool: "claude", Status: status.Working})
	m.maybeOpenRestorePrompt()
	if m.mode != modeList {
		t.Fatalf("prompt opened with no dead rows, mode=%v", m.mode)
	}
}

func TestSummaryScreenStandsForEveryCandidate(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"), deadSession("b", "beta", "id-b"))
	m.maybeOpenRestorePrompt()
	if got := len(m.restoreChosen()); got != 2 {
		t.Fatalf("summary should restore all candidates, got %d", got)
	}
}

// The summary names the sessions it offers rather than only counting them,
// and trims a long fleet to the first ten with the remainder stated.
func TestSummaryListsTheSessionsAndTrimsAtTen(t *testing.T) {
	sessions := make([]store.Session, 0, 12)
	for i := 0; i < 12; i++ {
		sessions = append(sessions, deadSession(
			fmt.Sprintf("s%02d", i), fmt.Sprintf("name%02d", i), fmt.Sprintf("id-%02d", i)))
	}
	m := restoreModel(sessions...)
	m.maybeOpenRestorePrompt()
	body := ansi.Strip(strings.Join(m.restoreBody(80), "\n"))
	for _, want := range []string{"name00", "name09", "… and 2 more"} {
		if !strings.Contains(body, want) {
			t.Fatalf("summary missing %q:\n%s", want, body)
		}
	}
	for _, over := range []string{"name10", "name11"} {
		if strings.Contains(body, over) {
			t.Fatalf("summary listed %q past the ten-row trim:\n%s", over, body)
		}
	}
}

func TestPickerStartsFullyTickedAndSpaceRemovesOne(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"), deadSession("b", "beta", "id-b"))
	m.maybeOpenRestorePrompt()
	m.handleRestorePromptKey(key("c"))
	if !m.restore.picking || len(m.restoreChosen()) != 2 {
		t.Fatalf("picker should open fully ticked, chosen=%d", len(m.restoreChosen()))
	}
	m.handleRestorePromptKey(key(" "))
	chosen := m.restoreChosen()
	if len(chosen) != 1 || chosen[0].ID != "b" {
		t.Fatalf("space should untick the row under the cursor, got %+v", chosen)
	}
}

func TestPickerAllKeyClearsThenTakesEverything(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"), deadSession("b", "beta", "id-b"))
	m.maybeOpenRestorePrompt()
	m.handleRestorePromptKey(key("c"))
	m.handleRestorePromptKey(key("a"))
	if got := len(m.restoreChosen()); got != 0 {
		t.Fatalf("a on a full set should clear it, got %d", got)
	}
	m.handleRestorePromptKey(key("a"))
	if got := len(m.restoreChosen()); got != 2 {
		t.Fatalf("a on a cleared set should take all, got %d", got)
	}
}

func TestEscFromPickerStepsBackToTheCountNotOut(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"))
	m.maybeOpenRestorePrompt()
	m.handleRestorePromptKey(key("c"))
	m.handleRestorePromptKey(key("esc"))
	if m.mode != modeRestorePrompt || m.restore.picking {
		t.Fatalf("esc should return to the count, mode=%v picking=%v", m.mode, m.restore.picking)
	}
	m.handleRestorePromptKey(key("esc"))
	if m.mode != modeList {
		t.Fatalf("esc from the count should dismiss, mode=%v", m.mode)
	}
}

func TestDegradedRowsAreCountedSeparately(t *testing.T) {
	m := restoreModel(deadSession("a", "alpha", "id-a"), deadSession("b", "beta", ""))
	if !m.resumesExactly(m.sessions[0]) {
		t.Fatal("a row with a conversation id should resume exactly")
	}
	if m.resumesExactly(m.sessions[1]) {
		t.Fatal("a row without a conversation id must not claim an exact resume")
	}
}

func TestDismissedOfferStaysDismissedOnTheNextLaunch(t *testing.T) {
	st := restoreStore(t)
	dead := []store.Session{deadSession("a", "alpha", "id-a"), deadSession("b", "beta", "id-b")}

	first := relaunch(st, dead...)
	first.maybeOpenRestorePrompt()
	if first.mode != modeRestorePrompt {
		t.Fatalf("expected the first launch to ask, mode=%v", first.mode)
	}
	first.handleRestorePromptKey(key("esc"))

	second := relaunch(st, dead...)
	second.maybeOpenRestorePrompt()
	if second.mode != modeList {
		t.Fatalf("the same dead rows were offered again, mode=%v", second.mode)
	}
}

// Answering from the picker settles every row it listed, not only the ticked
// ones: an offer the operator read and left unticked is an offer answered.
// Ticking none keeps the revive out of it, which needs a live tmux server.
func TestAnsweringThePickerSettlesEveryRowItOffered(t *testing.T) {
	st := restoreStore(t)
	dead := []store.Session{deadSession("a", "alpha", "id-a"), deadSession("b", "beta", "id-b")}

	first := relaunch(st, dead...)
	first.maybeOpenRestorePrompt()
	first.handleRestorePromptKey(key("c"))
	first.handleRestorePromptKey(key("a"))
	if got := len(first.restoreChosen()); got != 0 {
		t.Fatalf("expected an empty picker, chosen=%d", got)
	}
	first.handleRestorePromptKey(key("enter"))

	second := relaunch(st, dead...)
	second.maybeOpenRestorePrompt()
	if second.mode != modeList {
		t.Fatalf("rows left unticked were offered again, mode=%v", second.mode)
	}
}

func TestARowThatDiesAgainIsOfferedAgain(t *testing.T) {
	st := restoreStore(t)
	first := relaunch(st, deadSession("a", "alpha", "id-a"))
	first.maybeOpenRestorePrompt()
	first.handleRestorePromptKey(key("esc"))

	// Revived by hand and lost a second time: a different run of the agent,
	// and a loss the operator has never been asked about.
	again := deadSession("a", "alpha", "id-a")
	again.AgentLaunchedAt = launchedAt.Add(24 * time.Hour)
	second := relaunch(st, again)
	second.maybeOpenRestorePrompt()
	if second.mode != modeRestorePrompt {
		t.Fatalf("a second death was swallowed by the first answer, mode=%v", second.mode)
	}
}

func TestLedgerDropsMarksForRowsTheBoardNoLongerHas(t *testing.T) {
	st := restoreStore(t)
	first := relaunch(st, deadSession("a", "alpha", "id-a"))
	first.maybeOpenRestorePrompt()
	first.handleRestorePromptKey(key("esc"))

	// Session a is deleted; the next answer must not carry its mark forward.
	second := relaunch(st, deadSession("b", "beta", "id-b"))
	second.maybeOpenRestorePrompt()
	second.handleRestorePromptKey(key("esc"))

	decided, err := st.RestoreDecided()
	if err != nil {
		t.Fatalf("read decisions: %v", err)
	}
	if _, stale := decided["a"]; stale {
		t.Fatalf("mark for a deleted session survived: %+v", decided)
	}
	if _, kept := decided["b"]; !kept {
		t.Fatalf("expected b to be settled, got %+v", decided)
	}
}

// Quitting is not an answer: ctrl+c leaves the prompt without touching the
// ledger, so the next start still owes the operator the offer.
func TestQuittingTheOfferLeavesItUnanswered(t *testing.T) {
	st := restoreStore(t)
	dead := []store.Session{deadSession("a", "alpha", "id-a")}

	first := relaunch(st, dead...)
	first.maybeOpenRestorePrompt()
	first.handleRestorePromptKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	second := relaunch(st, dead...)
	second.maybeOpenRestorePrompt()
	if second.mode != modeRestorePrompt {
		t.Fatalf("a quit swallowed the offer, mode=%v", second.mode)
	}
}

// The pass that opens the prompt is the same pass that wrote the death, and
// the row it holds still carries the LastStatusAt from before it -- so the
// board's own copy of that field is a moving target between one start and the
// next, and a ledger keyed on it re-offered every answered row. Nothing about
// the row's agent moved here, so the answer stands.
func TestALaterStatusTimeDoesNotUnsettleARow(t *testing.T) {
	st := restoreStore(t)
	first := relaunch(st, deadSession("a", "alpha", "id-a"))
	first.maybeOpenRestorePrompt()
	first.handleRestorePromptKey(key("esc"))

	moved := deadSession("a", "alpha", "id-a")
	moved.LastStatusAt = launchedAt.Add(48 * time.Hour)
	second := relaunch(st, moved)
	second.maybeOpenRestorePrompt()
	if second.mode != modeList {
		t.Fatalf("a moved status time re-opened an answered offer, mode=%v", second.mode)
	}
}
