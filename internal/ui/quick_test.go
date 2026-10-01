// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/snippets"
)

const menuText = "carry on with the plan"

// bindMenuSnippet writes a snippets file whose only entry is on ctrl+alt+c.
func bindMenuSnippet(t *testing.T, m *Model, text string) {
	t.Helper()
	writeSnippets(t, m, []snippets.Snippet{{Key: "c", Label: "carry on", Text: text}})
}

// pressInMenu opens the hotkey menu and presses one key in it, through the
// board's own key handler, running whatever the key sent.
func pressInMenu(t *testing.T, m *Model, msg tea.KeyPressMsg) *Model {
	t.Helper()
	m.openQuickMode()
	if !m.quick.active {
		t.Fatalf("the hotkey menu did not open: %q", m.errBar.text)
	}
	updated, cmd := m.handleKey(msg)
	m = updated.(*Model)
	if cmd != nil {
		m.applyCmd(t, cmd)
	}
	return m
}

func letter(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// The menu is a menu: there is no box to type into, so every key that names
// no snippet does nothing, and nothing is ever sent that was not on a key.
func TestHotkeyMenuHasNoTextInput(t *testing.T) {
	m := buildModel(t)
	bindMenuSnippet(t, m, menuText)
	createSession(t, m, "answer-me", t.TempDir(), "")
	sess := m.sessionRows()[0]
	m.selectSessionRow(t, "answer-me")

	m.openQuickMode()
	for _, r := range "hello" {
		updated, cmd := m.handleKey(letter(r))
		m = updated.(*Model)
		if cmd != nil {
			t.Fatalf("%q in the menu started a command", r)
		}
	}
	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if cmd != nil || m.errBar.text != "" {
		t.Fatalf("enter in the menu did something: %q", m.errBar.text)
	}
	if !m.quick.active {
		t.Fatal("keys that name no snippet closed the menu")
	}
	if len(m.landings) != 0 || m.landings[sess.ID] != nil {
		t.Fatal("a key that names no snippet sent something")
	}
	bar := ansi.Strip(m.viewQuickBar(120, quickBarMaxRows))
	if strings.Contains(bar, "hello") || strings.Contains(bar, "❯") {
		t.Fatalf("the menu echoed typed text or drew a prompt:\n%s", bar)
	}
}

// The menu lists every snippet under the key that sends it from there, and
// the footer names the menu and how to leave it.
func TestHotkeyMenuListsTheSnippets(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, []snippets.Snippet{
		{Key: "c", Label: "continue", Text: "continue"},
		{Key: snippets.SectionKey, Label: "progress", Text: "summarise progress"},
		{Key: snippets.PlusMinusKey, Label: "admin merge and deploy", Text: "admin merge and deploy"},
	})
	createSession(t, m, "answer-me", t.TempDir(), "")
	m.selectSessionRow(t, "answer-me")
	m.openQuickMode()

	bar := ansi.Strip(m.viewQuickBar(120, quickBarMaxRows))
	for _, want := range []string{"send", "answer-me", "c continue", "§ progress", "± admin merge and deploy"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the menu is missing %q:\n%s", want, bar)
		}
	}
	footer := ansi.Strip(m.viewFooter())
	for _, want := range []string{"Hotkeys", "send snippet", "close"} {
		if !strings.Contains(footer, want) {
			t.Errorf("the footer is missing %q: %q", want, footer)
		}
	}
	if strings.Contains(footer, "tool:") {
		t.Errorf("the footer still offers a spawn tool: %q", footer)
	}
}

// A snippets file longer than the dock wraps onto its lines and counts the
// rest, rather than growing the dock and pushing the pane off the screen.
func TestHotkeyMenuCapsItsLines(t *testing.T) {
	m := buildModel(t)
	var many []snippets.Snippet
	for _, r := range "abcdefghjklnopqrstuvwxyz" {
		many = append(many, snippets.Snippet{Key: string(r), Label: "a fairly long label", Text: "x"})
	}
	writeSnippets(t, m, many)
	createSession(t, m, "answer-me", t.TempDir(), "")
	m.selectSessionRow(t, "answer-me")

	lines := m.quickMenuLines(60, 3)
	if len(lines) != 3 {
		t.Fatalf("%d lines, want the cap of 3", len(lines))
	}
	for _, line := range lines {
		if w := ansi.StringWidth(line); w > 60 {
			t.Fatalf("a line is %d wide, past the 60 it was given: %q", w, ansi.Strip(line))
		}
	}
	if !strings.Contains(ansi.Strip(lines[2]), "more in the key map") {
		t.Fatalf("the last line does not count what did not fit: %q", ansi.Strip(lines[2]))
	}
}

// With nothing bound the menu says where snippets come from.
func TestHotkeyMenuWithNoSnippetsNamesTheFile(t *testing.T) {
	m := buildModel(t)
	writeSnippets(t, m, nil)
	lines := m.quickMenuLines(200, quickBarMaxRows)
	if len(lines) != 1 || !strings.Contains(ansi.Strip(lines[0]), "snippets.json") {
		t.Fatalf("lines = %q, want one naming the file", lines)
	}
}

// In the menu a snippet answers to its bare key, and the chord still works.
func TestHotkeyMenuBareKeyAndChordBothSend(t *testing.T) {
	for name, msg := range map[string]tea.KeyPressMsg{
		"bare":  letter('c'),
		"chord": {Code: 'c', Mod: tea.ModCtrl | tea.ModAlt},
	} {
		t.Run(name, func(t *testing.T) {
			m := buildModel(t)
			bindMenuSnippet(t, m, menuText)
			createSession(t, m, "answer-me", t.TempDir(), "")
			sess := m.sessionRows()[0]
			if err := m.store.SetAcked(sess.ID, true); err != nil {
				t.Fatal(err)
			}
			m.selectSessionRow(t, "answer-me")

			m = pressInMenu(t, m, msg)
			if !strings.HasPrefix(m.errBar.text, "sent ") {
				t.Fatalf("errBar = %q, want the send acknowledged", m.errBar.text)
			}
			if m.landings[sess.ID] == nil || m.landings[sess.ID].text != menuText {
				t.Fatalf("landing = %+v, want %q pending", m.landings[sess.ID], menuText)
			}
			got, err := m.store.Get(sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Acked {
				t.Fatal("a send should clear the acked flag")
			}
			if !m.quick.active {
				t.Fatal("the menu should stay open after a send by default")
			}
		})
	}
}

// The two keys off the chord answer bare in the menu too: § without its alt,
// and ± as it always does.
func TestHotkeyMenuSendsTheSectionAndPlusMinusKeys(t *testing.T) {
	for _, key := range []string{snippets.SectionKey, snippets.PlusMinusKey} {
		t.Run(key, func(t *testing.T) {
			m := buildModel(t)
			writeSnippets(t, m, []snippets.Snippet{{Key: key, Text: "from " + key}})
			createSession(t, m, "answer-me", t.TempDir(), "")
			sess := m.sessionRows()[0]
			m.selectSessionRow(t, "answer-me")

			m = pressInMenu(t, m, tea.KeyPressMsg{Code: []rune(key)[0], Text: key})
			if m.landings[sess.ID] == nil || m.landings[sess.ID].text != "from "+key {
				t.Fatalf("%s did not send its snippet: %q", key, m.errBar.text)
			}
			if !m.quick.active {
				t.Fatalf("%s left the menu instead of sending", key)
			}
		})
	}
}

func TestHotkeyMenuSpaceAndEscClose(t *testing.T) {
	for name, msg := range map[string]tea.KeyPressMsg{
		"space": {Code: tea.KeySpace, Text: " "},
		"esc":   {Code: tea.KeyEsc},
	} {
		t.Run(name, func(t *testing.T) {
			m := buildModel(t)
			m.openQuickMode()
			updated, _ := m.handleKey(msg)
			if updated.(*Model).quick.active {
				t.Fatalf("%s did not close the menu", name)
			}
		})
	}
}

// A group has no pane: the menu says so, and a snippet key there sends
// nothing rather than spawning anything.
func TestHotkeyMenuOnAGroupSendsNothing(t *testing.T) {
	m := buildModel(t)
	bindMenuSnippet(t, m, menuText)
	groupAt(t, m, "work", t.TempDir())
	before := len(m.sessionRows())

	m = pressInMenu(t, m, letter('c'))
	if !strings.Contains(m.errBar.text, "select a session") {
		t.Fatalf("errBar = %q, want the group refusal", m.errBar.text)
	}
	if got := len(m.sessionRows()); got != before {
		t.Fatalf("a key on a group made %d sessions, want %d", got, before)
	}
	if bar := ansi.Strip(m.viewQuickBar(120, quickBarMaxRows)); !strings.Contains(bar, "select a session") {
		t.Fatalf("the menu on a group does not say to select a session:\n%s", bar)
	}
}

func TestHotkeyMenuDeadSessionSetsError(t *testing.T) {
	m := buildModel(t)
	bindMenuSnippet(t, m, menuText)
	createSession(t, m, "gone", t.TempDir(), "")
	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	m.selectSessionRow(t, "gone")

	m = pressInMenu(t, m, letter('c'))
	if m.errBar.text != "session is dead - press v to revive or R to restart" {
		t.Fatalf("err = %q", m.errBar.text)
	}
	if !m.quick.active {
		t.Fatal("the menu should stay open after a refused send")
	}
	if _, err := m.store.Get(sess.ID); err != nil {
		t.Fatalf("session record should survive: %v", err)
	}
}

func TestQuickCloseAfterSendDefaultsToStayingOpen(t *testing.T) {
	m := buildModel(t)
	if m.quickCloseAfterSend() {
		t.Fatal("the menu should stay open by default")
	}
	if err := m.store.SetSetting(quickCloseSetting, "close"); err != nil {
		t.Fatal(err)
	}
	if !m.quickCloseAfterSend() {
		t.Fatal("stored close choice should opt in")
	}
}

func TestHotkeyMenuClosesAfterSendWhenEnabled(t *testing.T) {
	m := buildModel(t)
	bindMenuSnippet(t, m, menuText)
	createSession(t, m, "answer-me", t.TempDir(), "")
	m.selectSessionRow(t, "answer-me")
	if err := m.store.SetSetting(quickCloseSetting, "close"); err != nil {
		t.Fatal(err)
	}

	m = pressInMenu(t, m, letter('c'))
	if !strings.HasPrefix(m.errBar.text, "sent ") {
		t.Fatalf("send: %q", m.errBar.text)
	}
	if m.quick.active {
		t.Fatal("the menu should close after a send when the setting is on")
	}
}

// SendText pastes and presses Enter, so on a shell row a snippet would run
// as a command: the target's tool is checked before anything is delivered,
// and the line never runs.
func TestHotkeyMenuNeverRunsASnippetAtAShell(t *testing.T) {
	m := buildModel(t)
	marker := filepath.Join(t.TempDir(), "executed")
	bindMenuSnippet(t, m, "touch "+marker)
	m.applyCmd(t, m.refreshCmd())
	sess := spawnTerminal(t, m)
	m.selectSessionRow(t, sess.Name)

	m = pressInMenu(t, m, letter('c'))
	if m.errBar.text != shellPromptHint(sess.Name) {
		t.Fatalf("err = %q, want the shell refusal", m.errBar.text)
	}
	// A shell that took the line would have created the file well inside
	// this window; a guard that holds leaves nothing to wait for.
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("the hotkey menu executed %q in the shell", marker)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// seedTwoGroups gives the list two rows so a selection move is observable.
func seedTwoGroups(t *testing.T, m *Model) {
	t.Helper()
	for _, name := range []string{"alpha", "beta"} {
		if err := m.store.CreateGroup(name, t.TempDir()); err != nil {
			t.Fatalf("create group: %v", err)
		}
	}
	m.applyCmd(t, m.refreshCmd())
	if len(m.rows) < 2 {
		t.Fatalf("rows = %d, want at least 2", len(m.rows))
	}
}

// The target follows the cursor: arrows still walk the list with the menu
// open.
func TestHotkeyMenuArrowsMoveTheSelection(t *testing.T) {
	m := buildModel(t)
	seedTwoGroups(t, m)
	m.openQuickMode()
	start := m.cursor
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor == start {
		t.Fatal("down did not move the selection")
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.cursor != start {
		t.Fatalf("up did not move the selection back: %d, want %d", m.cursor, start)
	}
	if !m.quick.active {
		t.Fatal("arrows closed the menu")
	}
}
