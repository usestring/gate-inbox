package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
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

func TestConversationDoesNotFallBackToRawOutput(t *testing.T) {
	m := drainFleet(t)
	m.preview = "private tool output"
	rows := m.previewLines(80, 20, "")
	for _, row := range rows {
		if strings.Contains(row.text, "private tool output") {
			t.Fatal("raw output leaked without a transcript")
		}
	}
}

func TestConversationReadsVerifiedAdoptedTranscriptAndRefreshes(t *testing.T) {
	m := drainFleet(t)
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

func TestConversationShowsWorkingSpinnerBelowNewestMessage(t *testing.T) {
	m := drainFleet(t)
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{key: conversationKey(sess.ID, sess.AgentSessionID), messages: []search.Message{
		{Role: "user", Text: "My question"},
		{Role: "assistant", Text: strings.Repeat("Reply line\n", 60)},
	}})
	width, height := m.previewPaneWidth(), m.previewPaneHeight()
	last := func() string {
		rows := m.conversationRows(width, height)
		return ansi.Strip(rows[len(rows)-1])
	}
	if strings.Contains(last(), startupFrames[0]) || m.needsLoaderTick() {
		t.Fatalf("waiting session shows a spinner: %q", last())
	}

	m.rows[m.cursor].sess.Status = status.Working
	cached := len(m.conversation.wrapped(width))
	if !m.needsLoaderTick() {
		t.Fatal("working conversation does not drive the loader tick")
	}
	if got := last(); !strings.HasPrefix(got, startupFrames[0]+" working") {
		t.Fatalf("newest row is %q, want the spinner", got)
	}
	m.startupPhase++
	if got := last(); !strings.HasPrefix(got, startupFrames[1]+" working") {
		t.Fatalf("spinner did not advance: %q", got)
	}
	if len(m.conversation.wrapped(width)) != cached {
		t.Fatal("spinner leaked into the wrapped cache")
	}

	m.keyScrollFocus(focusScrollTop)
	if top := ansi.Strip(m.conversationRows(width, height)[0]); !strings.Contains(top, "You") {
		t.Fatalf("top of the scroll lost the first message: %q", top)
	}

	m.conversation.messages, m.conversation.dirty = nil, true
	if got := last(); !strings.HasPrefix(got, startupFrames[1]+" working") {
		t.Fatalf("empty conversation hides the spinner: %q", got)
	}
}
