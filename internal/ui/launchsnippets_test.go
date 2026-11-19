package ui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/snippets"
)

func TestLaunchSnippetDeliversOnlyToANewSessionAndClosesMenu(t *testing.T) {
	for _, place := range []string{"list", "focus", "empty"} {
		t.Run(place, func(t *testing.T) {
			m := buildModel(t)
			if place != "empty" {
				createSession(t, m, "existing", t.TempDir(), "")
				m.selectSessionRow(t, "existing")
			}
			if place == "focus" {
				m.enterFocusOn(t, "existing")
			}
			prompt := "Review the working tree\nKeep the report concise"
			raw := `[{"key":"y","text":"Review the working tree\nKeep the report concise"}]`
			if err := os.WriteFile(snippets.LaunchPath(m.configDir()), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			m.snips = snippets.Set{Snippets: []snippets.Snippet{{Key: "y", Text: "yes"}}}
			before := len(m.sessions)
			press := func(msg tea.KeyPressMsg) {
				t.Helper()
				updated, _ := m.handleKey(msg)
				m = updated.(*Model)
			}
			press(menuTrigger())
			press(menuTrigger())
			if !m.quick.active || !m.quick.launch {
				t.Fatal("two leaders did not open launch snippets")
			}
			press(letter('y'))
			if m.quick.active || m.quick.launch {
				t.Fatal("a launch left the hotkey menu open")
			}
			if len(m.sessions) != before+1 {
				t.Fatalf("launch made %d sessions, want one: %s", len(m.sessions)-before, m.errBar.text)
			}
			sess, ok := m.selected()
			if !ok || sess.Name == "existing" || m.mode != modeFocus || !strings.Contains(sess.LaunchPrompt, prompt) {
				t.Fatalf("new pane did not receive the complete launch prompt: %+v, %s", sess, m.errBar.text)
			}
			if len(m.landings) != 0 {
				t.Fatal("a launch sent a snippet into an existing session")
			}
		})
	}
}

func TestLaunchMenuHonorsReboundLeader(t *testing.T) {
	m := buildModel(t)
	bindTestKey(t, m, keymap.QuickInput, "ctrl+g")
	for _, msg := range []tea.KeyPressMsg{key("ctrl+g"), key("ctrl+g"), key("ctrl+g")} {
		updated, _ := m.handleKey(msg)
		m = updated.(*Model)
	}
	if !m.quick.active || m.quick.launch {
		t.Fatal("the rebound leader did not return to ordinary snippets")
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.quick.active {
		t.Fatal("esc did not close the snippet menu")
	}
}
