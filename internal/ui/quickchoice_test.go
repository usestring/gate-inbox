package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/snippets"
	"github.com/usestring/gate-inbox/internal/status"
)

func TestChoiceForMapsAThroughC(t *testing.T) {
	for key, want := range map[string]int{"a": 1, "b": 2, "c": 3, "d": 0, "e": 0, "f": 0, "t": 0, "ab": 0, "": 0} {
		if got := choiceFor(key); got != want {
			t.Errorf("choiceFor(%q) = %d, want %d", key, got, want)
		}
	}
}

// While a dialog is held, b in the menu picks its second choice, ahead of a
// snippet bound to b that the dialog guard would only refuse.
func TestMenuLetterPicksTheHeldDialogsChoice(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{{Key: "b", Label: "bee", Text: "bee"}})
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting})
	m.rebuildRows()
	sess := sessionNamed(t, m, "ask")
	paintDialog(t, m, sess.ID)
	waitForHeldDialog(t, m, sess.ID)
	m.selectSessionRow(t, "ask")

	m = pressInMenu(t, m, letter('b'))
	if !strings.HasPrefix(m.errBar.text, "picked B. No") {
		t.Fatalf("err = %q, want the second choice picked", m.errBar.text)
	}
	if m.landings[sess.ID] != nil {
		t.Fatal("the snippet on b was sent as well")
	}
}

// With no dialog held the letter is the snippet's again.
func TestMenuLetterSendsItsSnippetWithoutADialog(t *testing.T) {
	m := buildModel(t)
	bindMenuSnippet(t, m, menuText)
	createSession(t, m, "plain", t.TempDir(), "")
	m.selectSessionRow(t, "plain")

	m = pressInMenu(t, m, letter('c'))
	if strings.HasPrefix(m.errBar.text, "picked") {
		t.Fatalf("c picked a choice on a pane with no dialog: %q", m.errBar.text)
	}
	waitForPaneText(t, m, m.sessionRows()[0].ID, menuText)
}

// waitForHeldDialog waits until the pane reads as one whole dialog. cat
// echoes the paste before it prints it back, so the legend is on screen a
// moment before the pane settles into a single readable dialog.
func waitForHeldDialog(t *testing.T, m *Model, sessID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sessID)
		if err != nil {
			t.Fatalf("capture %s: %v", sessID, err)
		}
		if held, ok := dialog.Inspect(ansi.Strip(pane)); ok && held.Cursor > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never settled into a readable dialog:\n%s", ansi.Strip(pane))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
