package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/search"
)

func TestConversationBoxesWrapAndSanitizeMessages(t *testing.T) {
	for _, width := range []int{12, 40, 80} {
		c := conversationView{dirty: true, messages: []search.Message{
			{Role: "user", Text: "Please fix the inbox."},
			{Role: "assistant", Text: "Fixed.\n\x1b]52;c;c2VjcmV0\a界界界界界界界界界界"},
		}}
		rows := c.wrapped(width)
		plain := ansi.Strip(strings.Join(rows, "\n"))
		for _, want := range []string{"You", "Assistant", "╭", "╰", "Fixed."} {
			if !strings.Contains(plain, want) {
				t.Fatalf("missing %q: %s", want, plain)
			}
		}
		for _, row := range rows {
			if textfmt.Width(row) > width {
				t.Fatalf("row exceeds %d columns: %q", width, row)
			}
			if strings.Contains(row, "52;") {
				t.Fatal("clipboard escape survived")
			}
		}
	}
}

func TestConversationScrollAndNativeTerminalSwitch(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	m.width, m.height = 80, 24
	sess, _ := m.selected()
	key := conversationKey(sess.ID, sess.AgentSessionID)
	m.preview = "hidden tool output"
	m.applyConversation(conversationMsg{key: key, messages: []search.Message{
		{Role: "user", Text: "My question"},
		{Role: "assistant", Text: strings.Repeat("Reply line\n", 60)},
	}})
	if !m.showsConversation() {
		t.Fatal("gate does not default to messages")
	}
	width, height := m.previewPaneWidth(), m.previewPaneHeight()
	m.conversationLines(width, height)
	m.keyScrollFocus(focusScrollTop)
	if m.conversation.offset == 0 {
		t.Fatal("Home did not scroll messages")
	}
	view := strings.Join(m.conversationRows(width, height), "\n")
	if !strings.Contains(view, "My question") || strings.Contains(view, "hidden tool output") {
		t.Fatal(view)
	}
	m.applyConversation(conversationMsg{key: "another session", messages: []search.Message{{Role: "assistant", Text: "wrong session"}}})
	if m.conversation.key != key {
		t.Fatal("late reply replaced selected conversation")
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if m.showsConversation() {
		t.Fatal("F2 did not reveal native terminal")
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if !m.showsConversation() {
		t.Fatal("F2 did not restore messages")
	}
	m.keyScrollFocus(focusScrollBottom)
	if m.conversation.offset != 0 {
		t.Fatal("End did not return to newest message")
	}
}

func TestConversationDoesNotFallBackToRawOutput(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	m.preview = "private tool output"
	rows := m.previewLines(80, 20, "")
	for _, row := range rows {
		if strings.Contains(row.text, "private tool output") {
			t.Fatal("raw output leaked without a transcript")
		}
	}
}

func TestConversationReadsVerifiedAdoptedTranscriptAndRefreshes(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	m.rows[m.cursor].sess.AgentSessionID = ""
	m.rows[m.cursor].sess.TmuxPaneID = "%123"
	sess, _ := m.selected()
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	first := `{"type":"user","message":{"role":"user","content":"My input"}}` + "\n"
	if err := os.WriteFile(path, []byte(first), 0600); err != nil {
		t.Fatal(err)
	}
	m.conversation.targets = map[string]search.Target{sess.ID: {Key: sess.ID, Tool: search.ToolClaude, AgentID: "verified-conversation", Path: path}}
	cmd := m.readConversation()
	if cmd == nil {
		t.Fatal("no read scheduled")
	}
	m.applyConversation(cmd().(conversationMsg))
	if m.conversation.err != nil {
		t.Fatal(m.conversation.err)
	}
	if len(m.conversation.messages) != 1 || m.conversation.messages[0].Text != "My input" {
		t.Fatalf("messages = %#v", m.conversation.messages)
	}
	msg := m.readConversation()().(conversationMsg)
	if !msg.unchanged {
		t.Fatal("unchanged file was reparsed")
	}
	m.applyConversation(msg)
	if err := os.WriteFile(path, []byte(first+`{"type":"assistant","message":{"role":"assistant","content":"My reply"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m.applyConversation(m.readConversation()().(conversationMsg))
	if len(m.conversation.messages) != 2 {
		t.Fatalf("appended reply missing: %#v", m.conversation.messages)
	}
}

func TestGateReplyKeepsConversationAndDraftAcrossViews(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	m.width, m.height = 80, 30
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{key: conversationKey(sess.ID, sess.AgentSessionID), messages: []search.Message{
		{Role: "user", Text: "Please change the preview"},
		{Role: "assistant", Text: "first\nsecond\nthird\nfourth\nexpanded detail\nlast detail"},
	}})
	m = pressFocused(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
	if !m.quick.active {
		t.Fatal("space did not open the reply box")
	}
	m = applyMsg(t, m, tea.PasteMsg{Content: "My draft"})
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	m = pressFocused(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})
	draft := "My draft\nq"
	if m.quick.input.Value() != draft {
		t.Fatalf("draft = %q", m.quick.input.Value())
	}
	view := ansi.Strip(m.frame())
	for _, want := range []string{"Please change the preview", "more lines", "My draft", "full view"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "expanded detail") {
		t.Fatal("composer did not shorten messages")
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyF3})
	if !strings.Contains(ansi.Strip(m.frame()), "expanded detail") {
		t.Fatal("F3 did not expand messages")
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if m.showsConversation() || strings.Contains(ansi.Strip(m.frame()), "My draft") {
		t.Fatal("terminal view retained composer")
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if m.quick.input.Value() != draft || !m.showsConversation() {
		t.Fatal("toggle lost draft or conversation")
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if focusedID(t, m) != sess.ID {
		t.Fatal("editing moved the reply target")
	}
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.quick.active || !m.showsConversation() {
		t.Fatal("Escape should close only the composer")
	}
}

func TestGateReplySubmissionAdvancesOnlyAfterSuccessfulSend(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	m = pressFocused(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
	sess, _ := m.selected()
	paintDialog(t, m, sess.ID)
	m.quick.input.SetValue("Please continue")
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if focusedID(t, m) != sess.ID || m.quick.input.Value() != "Please continue" || m.errBar.text == "" {
		t.Fatal("a native dialog should refuse the send and retain its draft")
	}
	m = pressGate(t, gateFleet(t))
	m = pressFocused(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
	sess, _ = m.selected()
	m.quick.input.SetValue("Please continue")
	m = pressFocused(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.errBar.text != "" {
		t.Fatal(m.errBar.text)
	}
	waitForPaneText(t, m, sess.ID, "Please continue")
	if focusedID(t, m) == sess.ID || m.quick.active {
		t.Fatal("successful send did not advance and close composer")
	}
}

func TestShortenedConversationWrapsWithinNarrowScreens(t *testing.T) {
	for _, width := range []int{12, 40, 80} {
		c := conversationView{dirty: true, compact: true, messages: []search.Message{{Role: "assistant", Text: strings.Repeat("界 long reply\n", 40)}}}
		rows := c.wrapped(width)
		if len(rows) != 8 {
			t.Fatalf("shortened message uses %d rows", len(rows))
		}
		for _, row := range rows {
			if textfmt.Width(row) > width {
				t.Fatalf("row exceeds %d: %q", width, row)
			}
		}
	}
}

func TestGateReplyReservesConversationOnShortScreens(t *testing.T) {
	m := pressGate(t, gateFleet(t))
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{key: conversationKey(sess.ID, sess.AgentSessionID), messages: []search.Message{{Role: "assistant", Text: "Recent reply"}}})
	m = pressFocused(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
	m.quick.input.SetValue(strings.Repeat("Long draft line\n", 15))
	for _, size := range [][2]int{{40, 12}, {60, 20}, {120, 40}} {
		m.width, m.height = size[0], size[1]
		view := ansi.Strip(m.frame())
		if !strings.Contains(view, "Recent reply") || !strings.Contains(view, "Long draft line") {
			t.Fatalf("%dx%d lost conversation or draft:\n%s", m.width, m.height, view)
		}
		if len(strings.Split(view, "\n")) > m.height {
			t.Fatalf("view exceeds %d rows", m.height)
		}
	}
	m = pressFocused(t, m, ctrlBackslash())
	if m.quick.active || m.gate.on {
		t.Fatal("leaving the gate retained its draft")
	}
}
