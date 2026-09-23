package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// tmuxHintModel is a store-backed manager with a terminal wide enough to draw
// the card whole, armed the way Init arms it.
func tmuxHintModel(t *testing.T) *Model {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &Model{store: st, mode: modeList, width: 120, height: 50, tmuxHintArmed: true}
}

func linksFinding() tmux.ConfigFinding {
	return tmux.ConfigFinding{
		Key:    tmux.FindingHyperlinks,
		Detail: "links are flattened",
		Fix:    "set -as terminal-features 'xterm*:hyperlinks'",
	}
}

func clipboardFinding() tmux.ConfigFinding {
	return tmux.ConfigFinding{
		Key:    tmux.FindingSetClipboard,
		Detail: "set-clipboard is external",
		Fix:    "set -s set-clipboard on",
	}
}

func TestTheCardOpensOnceForASetOfFindings(t *testing.T) {
	m := tmuxHintModel(t)
	m.maybeOpenTmuxHint([]tmux.ConfigFinding{linksFinding()})
	if m.mode != modeTmuxHint {
		t.Fatalf("expected the note to open, mode=%v", m.mode)
	}

	m.handleTmuxHintKey(key("x"))
	if m.mode != modeList {
		t.Fatalf("any key should land on the list, mode=%v", m.mode)
	}

	// A later start reads the same store: a setting already put in front of
	// this operator does not greet them again.
	m.tmuxHintArmed = true
	m.maybeOpenTmuxHint([]tmux.ConfigFinding{linksFinding()})
	if m.mode != modeList {
		t.Fatalf("the note came back for a finding already shown, mode=%v", m.mode)
	}
}

// A config that grows a new problem is worth raising even though its other
// findings are old news -- and the card then shows the whole set, because the
// operator is in ~/.tmux.conf either way.
func TestANewFindingRaisesTheCardWithTheWholeSet(t *testing.T) {
	m := tmuxHintModel(t)
	m.maybeOpenTmuxHint([]tmux.ConfigFinding{linksFinding()})
	m.handleTmuxHintKey(key("x"))

	m.tmuxHintArmed = true
	m.maybeOpenTmuxHint([]tmux.ConfigFinding{linksFinding(), clipboardFinding()})
	if m.mode != modeTmuxHint {
		t.Fatalf("a new finding did not raise the note, mode=%v", m.mode)
	}
	if len(m.tmuxHint.findings) != 2 {
		t.Fatalf("card shows %d findings, want the whole set", len(m.tmuxHint.findings))
	}
}

// Once a run, and only at startup: Init arms the read and the first delivery
// spends it, so nothing that later hands the model a set of findings can put
// the card back over a board the operator is working on.
func TestTheReadIsSpentAfterTheFirstDelivery(t *testing.T) {
	m := tmuxHintModel(t)
	m.maybeOpenTmuxHint([]tmux.ConfigFinding{linksFinding()})
	m.handleTmuxHintKey(key("x"))

	// No re-arming: this stands for a second delivery inside the same run.
	m.maybeOpenTmuxHint([]tmux.ConfigFinding{linksFinding(), clipboardFinding()})
	if m.mode != modeList {
		t.Fatalf("a second delivery in one run reopened the card, mode=%v", m.mode)
	}
}

func TestAHealthyTmuxSaysNothing(t *testing.T) {
	m := tmuxHintModel(t)
	m.maybeOpenTmuxHint(nil)
	if m.mode != modeList {
		t.Fatalf("raised a note with nothing to report, mode=%v", m.mode)
	}
	if seen, err := m.store.Setting(tmuxHintSeenSetting); err != nil || seen != "" {
		t.Fatalf("spent the flag with nothing to show, got %q err %v", seen, err)
	}
}

// The first run's welcome card owns the screen, and an operator mid-task owns
// it too: the note waits for the next start rather than painting over either.
func TestTheNoteWaitsRatherThanTakingAnotherScreen(t *testing.T) {
	m := tmuxHintModel(t)
	m.mode = modeWelcome
	m.maybeOpenTmuxHint([]tmux.ConfigFinding{linksFinding()})
	if m.mode != modeWelcome {
		t.Fatalf("the note took the welcome card's screen, mode=%v", m.mode)
	}

	// Deferring must not spend the finding. The read happens once a run, so
	// what proves this is a later run over the same store, not a second call
	// in this one.
	next := &Model{store: m.store, mode: modeList, width: 120, height: 50, tmuxHintArmed: true}
	next.maybeOpenTmuxHint([]tmux.ConfigFinding{linksFinding()})
	if next.mode != modeTmuxHint {
		t.Fatalf("a deferred note was lost to the next start, mode=%v", next.mode)
	}
}

// The fix is what the operator came for: it renders whole and on its own row,
// because a wrapped config line copies wrong.
func TestTheCardShowsEachFixLineUnwrapped(t *testing.T) {
	m := tmuxHintModel(t)
	m.maybeOpenTmuxHint([]tmux.ConfigFinding{linksFinding(), clipboardFinding()})

	frame := ansi.Strip(m.viewTmuxHint())
	for _, want := range []string{
		"set -as terminal-features 'xterm*:hyperlinks'",
		"set -s set-clipboard on",
		"~/.tmux.conf",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("card never shows %q:\n%s", want, frame)
		}
	}
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "set -as terminal-features") && !strings.Contains(line, "hyperlinks'") {
			t.Errorf("the fix line was wrapped: %q", line)
		}
	}
}

// A finding with no line to add -- the mouse trade -- must not drag the
// "put these in ~/.tmux.conf" footer along with it.
func TestAFindingWithNoFixCarriesNoConfFooter(t *testing.T) {
	body := strings.Join(tmuxHintLines([]tmux.ConfigFinding{{
		Key:    tmux.FindingMouseClicks,
		Detail: "mouse mode takes the click",
	}}, 60), "\n")
	if strings.Contains(ansi.Strip(body), "~/.tmux.conf") {
		t.Fatalf("told the operator to edit a file for a finding with no line:\n%s", body)
	}
}

func TestSeenKeysKeepsWhatWasAlreadyRecorded(t *testing.T) {
	if got := seenKeys([]tmux.ConfigFinding{clipboardFinding()}, "hyperlinks"); got != "hyperlinks,set-clipboard" {
		t.Fatalf("seenKeys = %q", got)
	}
	if got := seenKeys([]tmux.ConfigFinding{linksFinding()}, ""); got != "hyperlinks" {
		t.Fatalf("seenKeys from empty = %q", got)
	}
}
