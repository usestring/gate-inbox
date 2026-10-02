package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

func conversationInputModel(t *testing.T, tool, pane string, cursor paneCursor) *Model {
	t.Helper()
	sess := store.Session{ID: "composer", Tool: tool}
	m := &Model{
		engine: liveEngine(t), mode: modeFocus, focusView: focusViewConversation, compressedFocus: true, cursorOn: true,
		rows: []treeRow{{sess: sess}}, preview: pane,
		conversation: &conversationView{key: conversationKey(sess.ID, ""), dirty: true,
			messages: []search.Message{{Role: "assistant", Text: "A shortened reply stays above your input."}}},
	}
	m.pane.forID, m.pane.cursor = sess.ID, cursor
	return m
}

func conversationInputText(lines []contentLine) string {
	rows := make([]string, len(lines))
	for i, line := range lines {
		rows[i] = line.text
	}
	return ansi.Strip(strings.Join(rows, "\n"))
}

func TestConversationFocusHoverUsesVisibleTranscriptAboveComposer(t *testing.T) {
	m := conversationInputModel(t, "codex", "output\n\n› draft\n  model\n", paneCursor{x: 7, y: 2, ok: true})
	c := m.conversation
	c.compact, c.hovered = true, -1
	c.messages = []search.Message{
		{Role: "assistant", Text: strings.Repeat("older line\n", 12)},
		{Role: "user", Text: strings.Repeat("middle line\n", 12)},
		{Role: "assistant", Text: "newest reply"},
	}
	lines := m.previewLines(50, 18, "")
	middleRow := -1
	for row, line := range lines {
		if strings.Contains(ansi.Strip(line.text), "middle line") {
			middleRow = row
			break
		}
	}
	if middleRow < 0 {
		t.Fatal("middle turn is not visible")
	}
	if !m.updateConversationHover(m.pane.box.x+2, m.pane.box.y+middleRow) || c.hovered != 1 {
		t.Fatalf("hovered group = %d, want the visible middle turn", c.hovered)
	}
	m.previewLines(50, 18, "")
	if !m.updateConversationHover(m.pane.box.x+2, m.pane.box.y+c.liveTop) || c.hovered != -1 {
		t.Fatalf("composer hover expanded transcript group %d", c.hovered)
	}
}

func TestConversationFocusPinsEachHarnessComposerAndCaret(t *testing.T) {
	cases := []struct {
		tool, pane string
		cursor     paneCursor
	}{
		{"claude", "tool output\n────────────────────\n❯ draft reply\n  second line\n────────────────────\n  esc to interrupt\n", paneCursor{x: 6, y: 3, ok: true}},
		{"codex", "tool output\n\n› draft reply\n  second line\n\n  gpt-6\n", paneCursor{x: 6, y: 3, ok: true}},
		{"opencode", "tool output\n\n  ┃ draft reply\n  ┃ second line\n  ┃ Build auto\n  ╹▀▀▀▀▀▀▀▀\n  ctrl+p commands\n", paneCursor{x: 8, y: 3, ok: true}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			m := conversationInputModel(t, tc.tool, tc.pane, tc.cursor)
			m.conversation.offset = 100
			lines := m.previewLines(50, 15, "")
			got := conversationInputText(lines)
			for _, want := range []string{"draft reply", "second line", "shortened reply"} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "tool output") {
				t.Fatalf("transcript noise was not compressed:\n%s", got)
			}
			row, col, ok := m.cursorCell(m.pane.box.height)
			if !ok || col != tc.cursor.x || !strings.Contains(ansi.Strip(lines[row].text), "second line") {
				t.Fatalf("caret does not map onto the visible input: row=%d col=%d ok=%v", row, col, ok)
			}
			lit := lines[row].text
			m.cursorOn = false
			dark := m.previewLines(50, 15, "")[row].text
			if lit == dark {
				t.Fatal("input caret does not blink")
			}
			for _, line := range lines {
				if ansi.StringWidth(line.text) > 50 {
					t.Fatalf("row exceeds panel width: %q", line.text)
				}
			}
		})
	}
}

func TestConversationFocusMirrorsCapturedDialogs(t *testing.T) {
	cases := []struct{ tool, file, want string }{
		{"claude", "../dialog/testdata/claude-2.1.284-w40-free-text-typed.ansi", "Enter to select"},
		{"claude", "../dialog/testdata/claude-2.1.286-w40-multiselect.ansi", "Submit"},
		{"claude", "../dialog/testdata/claude-2.1.283-tabs-w44-submit-wrapped.ansi", "Submit"},
		{"claude", "../dialog/testdata/claude-2.1.286-w40-permission-bash.ansi", "Esc to cancel"},
		{"claude", "../status/testdata/claude-trust-prompt.txt", "trust"},
		{"opencode", "testdata/opencode-question-dialog.txt", "enter submit"},
		{"opencode", "../status/testdata/opencode-permission-bash.txt", "Permission required"},
	}
	for _, tc := range cases {
		t.Run(filepath.Base(tc.file), func(t *testing.T) {
			raw, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			m := conversationInputModel(t, tc.tool, string(raw), paneCursor{})
			got := conversationInputText(m.previewLines(200, 80, ""))
			if !strings.Contains(got, tc.want) {
				t.Fatalf("live dialog control %q missing:\n%s", tc.want, got)
			}
			if strings.Contains(got, "shortened reply") {
				t.Fatalf("transcript crowds the live dialog:\n%s", got)
			}
			want := strings.TrimRight(ansi.Strip(string(raw)), "\n")
			for _, row := range strings.Split(want, "\n") {
				if !strings.Contains(got, strings.TrimRight(ansi.Strip(previewLine(row, 200)), " ")) {
					t.Fatalf("captured dialog row disappeared: %q", row)
				}
			}
		})
	}
}

func TestConversationFocusMirrorsCodexQuestionsApprovalsAndUnknownMenus(t *testing.T) {
	for _, pane := range []string{
		"Choose a color.\n\n› 1. Blue\n  2. Red\n\n  tab to add notes | enter to submit answer | esc to interrupt\n",
		"$ echo hello\n\n› 1. Yes, proceed (y)\n  2. No (esc)\n\n  Press enter to confirm or esc to cancel\n",
		"Select a command\n  /model\n  /settings\n  enter to select\n",
	} {
		m := conversationInputModel(t, "codex", pane, paneCursor{x: 2, y: 2, ok: true})
		got := conversationInputText(m.previewLines(80, 20, ""))
		for _, row := range strings.Split(strings.TrimRight(pane, "\n"), "\n") {
			if !strings.Contains(got, row) {
				t.Fatalf("dialog/menu row disappeared: %q\n%s", row, got)
			}
		}
	}
}

func TestConversationListPreviewDoesNotExposeLiveDraft(t *testing.T) {
	m := conversationInputModel(t, "claude", "❯ unsent draft\n", paneCursor{x: 5, y: 0, ok: true})
	m.mode = modeList
	if got := conversationInputText(m.previewLines(50, 15, "")); strings.Contains(got, "unsent draft") {
		t.Fatalf("list preview exposed unsent input:\n%s", got)
	}
}

func TestConversationFocusRendersCaretOnPromptMarker(t *testing.T) {
	m := conversationInputModel(t, "claude", "❯\n", paneCursor{x: 0, y: 0, ok: true})
	got := conversationInputText(m.previewLines(50, 15, ""))
	if !strings.Contains(got, "❯") || !strings.Contains(got, "shortened reply") {
		t.Fatalf("missing live prompt or conversation:\n%s", got)
	}
}

func TestConversationFocusMouseTargetsOnlyTheLiveComposer(t *testing.T) {
	m := conversationInputModel(t, "opencode", "tool output\n\n  ┃ draft reply\n  ╹▀▀▀▀\n", paneCursor{x: 10, y: 2, ok: true})
	m.previewLines(50, 15, "")
	m.pane.mouse = true
	c := m.conversation
	row := c.liveTop + m.pane.cursor.y - c.liveStart
	if row+m.paneRowOffset(15) != m.pane.cursor.y {
		t.Fatal("mouse row does not map to the live composer")
	}
	box := m.pane.box
	m.handleFocusMouse(tea.MouseClickMsg{Button: tea.MouseLeft, X: box.x + 10, Y: box.y + row})
	if !m.pending.active {
		t.Fatal("a composer click was not forwarded to its harness")
	}
	m.pending = pendingClick{}
	m.handleFocusMouse(tea.MouseClickMsg{Button: tea.MouseLeft, X: box.x + 10, Y: box.y})
	if m.pending.active || !m.sel.active {
		t.Fatal("a transcript click was sent to the harness instead of selecting text")
	}
}
