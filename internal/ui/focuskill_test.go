package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
)

func ctrlX() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl} }

func TestRefreshReleasesFocusWhenThePaneDies(t *testing.T) {
	m := drainFleet(t)
	m.enterFocusOn(t, "ask")
	sess, _ := m.selected()
	m.applyCmd(t, m.refreshCmd())
	if m.mode != modeFocus {
		t.Fatal("a live pane lost focus on refresh")
	}

	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	stored, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != status.Dead || sessionGone(m.sessions, sess.ID) {
		t.Fatal("the refresh must retain the dead managed session")
	}
	if m.mode != modeList {
		t.Fatalf("detecting a dead pane left mode %v, want list navigation", m.mode)
	}
	if m.heldAckID != "" {
		t.Fatal("the dead pane retained its held acknowledgement")
	}
}

// A triage drain whose focused pane exits hands over the next session in the
// queue rather than following the dead row down to the foot of the rail.
func TestTriageAdvancesWhenTheFocusedPaneExits(t *testing.T) {
	m := focusedKillFleet(t, true)
	m.enterFocusOn(t, "ask")
	sess, _ := m.selected()
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	if m.mode != modeFocus || focusedName(t, m) != "broke" {
		t.Fatalf("the pane exiting left mode %v on %q, want focus on broke", m.mode, focusedName(t, m))
	}
}

// With nothing left to hand over the drain lands on the list, and the cursor
// on the head of the queue rather than on the session that just exited.
func TestTriageLeavesTheExitedRowWhenTheQueueIsEmpty(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"done": status.Waiting,
	})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "ask")
	sess, _ := m.selected()
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatal(err)
	}
	refresh, ok := m.refreshCmd()().(refreshMsg)
	if !ok {
		t.Fatal("the refresh did not report the board")
	}
	// Already handed over this drain, so it is on the rail but not the
	// queue. The mark covers the state the refresh reports, since the poller
	// re-derives every status on its way there.
	for _, other := range refresh.sessions {
		if other.Name == "done" {
			m.mute(other)
		}
	}
	updated, _ := m.Update(refresh)
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("an empty queue entered %q", focusedName(t, m))
	}
	if got := focusedName(t, m); got != "done" {
		t.Fatalf("the cursor landed on %q, want the queue's head rather than the exited session", got)
	}
}

// focusedKillFleet is a triage queue with a real pane behind every row, sized
// so the dialog and the focused frame both have somewhere to draw.
func focusedKillFleet(t *testing.T, triage bool) *Model {
	t.Helper()
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"busy":  status.Working,
	})
	m.triage = triage
	m.rebuildRows()
	return m
}

// ctrl+x does not act; it asks. Every other key in a focused pane reaches the
// agent, so this one is reachable mid-sentence by a slip of the hand, and what
// it ends is somebody's running work.
func TestFocusedArchiveKeyAsksBeforeItActs(t *testing.T) {
	m := focusedKillFleet(t, true)
	m.enterFocusOn(t, "ask")
	sess, _ := m.selected()

	updated, _ := m.handleKey(ctrlX())
	m = updated.(*Model)

	frame := ansi.Strip(m.frame())
	if !strings.Contains(frame, "Kill session") {
		t.Fatalf("ctrl+x painted no dialog:\n%s", frame)
	}
	if !strings.Contains(frame, "kill ask") {
		t.Fatalf("the dialog does not name the session it would end:\n%s", frame)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("ctrl+x ended the session before anybody answered")
	}
}

// Declining is a mis-key taken back. Landing on the list would cost the
// operator the pane they were typing into, which is the same loss the
// confirmation exists to prevent.
func TestDecliningTheFocusedKillPutsTheKeysBackInThePane(t *testing.T) {
	m := focusedKillFleet(t, true)
	m.enterFocusOn(t, "ask")
	sess, _ := m.selected()

	updated, _ := m.handleKey(ctrlX())
	m = updated.(*Model)
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = updated.(*Model)

	if m.mode != modeFocus {
		t.Fatalf("declining left mode %v instead of the pane", m.mode)
	}
	if !m.tmux.Exists(sess.ID) {
		t.Fatal("the session died despite the answer being no")
	}
	frame := ansi.Strip(m.frame())
	if !strings.Contains(frame, "stop triage") {
		t.Fatalf("the frame after declining is not the focused frame:\n%s", frame)
	}
	if strings.Contains(frame, "Kill session") {
		t.Fatalf("the dialog is still up after esc:\n%s", frame)
	}
}

// Confirmed, it ends the pane and the manager takes the keys back.
func TestConfirmingTheFocusedKillEndsTheSession(t *testing.T) {
	m := focusedKillFleet(t, false)
	m.enterFocusOn(t, "ask")
	sess, _ := m.selected()

	updated, _ := m.handleKey(ctrlX())
	m = updated.(*Model)
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(*Model)

	if m.tmux.Exists(sess.ID) {
		t.Fatalf("the confirmed kill left the session alive: %q", m.errBar.text)
	}
	if m.mode != modeList {
		t.Fatalf("outside triage the kill left mode %v rather than the list", m.mode)
	}
}

// The point of the key: killing mid-drain keeps draining. The queue moves on
// to the next session that needs a person, and never back to the one just
// killed -- whose row is still there, reading dead.
func TestTheFocusedKillCarriesOnDrainingTheTriageQueue(t *testing.T) {
	m := focusedKillFleet(t, true)
	m.enterFocusOn(t, "ask")
	killed, _ := m.selected()

	updated, _ := m.handleKey(ctrlX())
	m = updated.(*Model)
	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("the confirmed kill in triage returned no command, so nothing advanced")
	}

	if m.mode != modeFocus {
		t.Fatalf("the kill dropped out of the drain into mode %v: %s", m.mode, m.errBar.text)
	}
	if got := focusedName(t, m); got != "broke" {
		t.Fatalf("the drain landed on %q, want the next session needing input", got)
	}
	if m.tmux.Exists(killed.ID) {
		t.Fatal("the session the drain moved on from is still alive")
	}
	frame := ansi.Strip(m.frame())
	if !strings.Contains(frame, "stop triage") {
		t.Fatalf("the frame after the kill is not a focused frame:\n%s", frame)
	}
}

// A hotkey nobody can find is not shipped: the focused footer names it, and
// it is named there rather than left to the key map because it is the one
// key in that tier that takes something away.
func TestTheFocusedFooterNamesTheArchive(t *testing.T) {
	m := shotModel()
	m.width, m.height = 200, 50
	m.mode = modeFocus

	footer := ansi.Strip(m.viewFooter())
	if !strings.Contains(footer, "ctrl+x") || !strings.Contains(footer, "kill") {
		t.Errorf("the focused footer never names the key that ends the session:\n%s", footer)
	}
}

// And the key map names it too, with what it does to the queue. The catalog
// is taller than any terminal, so this reads the page the key sits on rather
// than the first screen of it.
func TestTheKeyMapNamesTheFocusedKill(t *testing.T) {
	m := shotModel()
	m.width, m.height = 200, 50
	m.mode = modeHelp

	deadline := m.helpScrollLimit() + 1
	for step := 0; ; step++ {
		if strings.Contains(ansi.Strip(m.frame()), "ctrl+x") {
			return
		}
		if step >= deadline {
			t.Fatalf("? never names ctrl+x, over %d pages of the key map", step)
		}
		updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m = updated.(*Model)
	}
}

// The rebind is a trade in both directions. ctrl+x is claimed from the pane --
// TestFocusedArchiveKeyAsksBeforeItActs is that half, since a dialog is proof
// the key never forwarded -- and alt+x is handed back to it, which is the half
// that would otherwise rot silently into a key that does nothing at all.
func TestAltXReachesTheAgentNowThatArchiveMovedToCtrl(t *testing.T) {
	m := focusedKillFleet(t, true)
	m.enterFocusOn(t, "ask")

	altX := tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt}
	updated, _ := m.handleKey(altX)
	m = updated.(*Model)

	if frame := ansi.Strip(m.frame()); strings.Contains(frame, "Kill session") {
		t.Fatalf("alt+x still archives, so the agent never got the key back:\n%s", frame)
	}
	if _, ok := focusKeyCommand("gi_x", altX); !ok {
		t.Error("alt+x has no forwardable form, so freeing it bought the agent nothing")
	}
}

// Paging must not skip a row: fitBody draws its markers over the first and
// last lines of the window, so a page step that ignores them hides one row
// at every boundary.
func TestPagingTheKeyMapShowsEveryBinding(t *testing.T) {
	m := shotModel()
	m.width, m.height = 200, 50
	m.mode = modeHelp

	want := map[string]bool{}
	for _, section := range helpSectionsOf() {
		for _, row := range section.rows {
			if row.text != "" {
				want[row.text] = true
			}
		}
	}
	seen := map[string]bool{}
	for step := 0; step <= m.helpScrollLimit()+1; step++ {
		frame := ansi.Strip(m.frame())
		for text := range want {
			if strings.Contains(frame, text) {
				seen[text] = true
			}
		}
		updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m = updated.(*Model)
	}
	for text := range want {
		if !seen[text] {
			t.Errorf("paging never showed %q", text)
		}
	}
}
