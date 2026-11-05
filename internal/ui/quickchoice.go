package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/store"
)

// choiceKeys are the menu keys that pick a held dialog's numbered choices:
// a is 1 through e is 5. While the target pane holds a choice dialog they
// come ahead of any snippet on the same letter, since a snippet sent there
// would only be refused by the dialog guard. Once no dialog is held, the
// letters are the snippets' again.
const choiceKeys = "abcde"

// choiceFor reads a menu key as the 1-based choice it picks, or 0.
func choiceFor(key string) int {
	if len(key) != 1 {
		return 0
	}
	return strings.Index(choiceKeys, key) + 1
}

// pickChoice presses the n-th choice of the dialog sess's pane holds. ok is
// false when the pane holds no dialog with that choice, so the key falls
// through to the snippet it names. On a multi-select the press ticks or
// unticks that box; the operator submits it.
//
// The operator is the person a guarded prompt waits on, so a permission
// prompt is answered here like any other dialog.
func (m *Model) pickChoice(sess store.Session, n int) (tea.Cmd, bool) {
	if n == 0 || sess.Archived || m.isShell(sess.Tool) {
		return nil, false
	}
	// The status rules, not the legend alone, say a dialog is held: a
	// resolved one left in scrollback still reads, and Enter keyed at the
	// prompt under it would submit whatever is typed there.
	hold, pane := m.dialogHold(sess)
	if hold == "" {
		return nil, false
	}
	held, ok := dialog.Inspect(pane)
	if !ok || n > len(held.Options) {
		return nil, false
	}
	at, keys := held.Cursor, []string(nil)
	if held.OnSubmit && held.FreeText > 0 {
		// The marker on Submit is on no numbered row; Up puts it back on
		// the free-text row the rest is counted from.
		at, keys = held.FreeText, []string{"Up"}
	}
	moves := dialog.SelectKeys(at, n)
	if moves == nil {
		m.errBar.text = m.displayName(sess) + "'s dialog shows no selection to count from — pick it in the pane"
		return nil, true
	}
	if err := m.tmux.SendKeys(sess.ID, append(keys, moves...)...); err != nil {
		m.errBar.text = err.Error()
		return nil, true
	}
	verb := "picked"
	if held.MultiSelect {
		verb = "toggled"
	}
	m.errBar.text = verb + " " + string(rune('A'+n-1)) + ". " + held.Options[n-1] + " in " + m.displayName(sess)
	m.requestRefresh()
	return nil, true
}
