package ui

import (
	"os"
	"path/filepath"
	"regexp"
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
	if err := os.WriteFile(path, []byte(first+`{"type":"assistant","timestamp":"2026-10-01T12:00:00Z","message":{"role":"assistant","content":"My reply","usage":{"input_tokens":123}}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m.applyConversation(m.readConversation()().(conversationMsg))
	if len(m.conversation.messages) != 2 {
		t.Fatalf("appended reply missing: %#v", m.conversation.messages)
	}
	if usage := m.conversation.usage; !usage.Known || usage.Tokens != 123 {
		t.Fatalf("appended usage missing: %+v", usage)
	}
}

func TestShortenedConversationWrapsWithinNarrowScreens(t *testing.T) {
	for _, width := range []int{12, 40, 80} {
		// The older group shortens; the newest stays full even when long.
		c := conversationView{dirty: true, compact: true, hovered: -1, messages: []search.Message{
			{Role: "assistant", Text: strings.Repeat("界 long reply\n", 40)},
			{Role: "user", Text: "ok"},
		}}
		rows := c.wrapped(width)
		// Shortened older box (heading + 4 lines + more + footer + gap) plus
		// the full newest box (heading + 1 line + footer + gap).
		if len(rows) != 12 {
			t.Fatalf("shortened conversation uses %d rows", len(rows))
		}
		for _, row := range rows {
			if textfmt.Width(row) > width {
				t.Fatalf("row exceeds %d: %q", width, row)
			}
		}
	}
}

func TestConversationViewChoiceSurvivesQuickPrompt(t *testing.T) {
	m := drainFleet(t)
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{key: conversationKey(sess.ID, sess.AgentSessionID), messages: []search.Message{
		{Role: "assistant", Text: "first\nsecond\nthird\nfourth\nexpanded detail\nlast detail"},
		{Role: "user", Text: "looks good"},
	}})
	view := func() string {
		return ansi.Strip(strings.Join(m.conversationRows(80, 20), "\n"))
	}
	if !m.conversation.compact || !strings.Contains(view(), "more lines") {
		t.Fatal("conversation did not start shortened")
	}
	m.toggleConversation()
	if m.conversation.compact || !strings.Contains(view(), "expanded detail") {
		t.Fatal("F3 did not expand the conversation")
	}
	m.openQuickMode()
	if !m.quick.active || m.conversation.compact || !strings.Contains(view(), "expanded detail") {
		t.Fatal("quick prompt changed the chosen full view")
	}
	m.toggleConversation()
	if !m.conversation.compact || !strings.Contains(view(), "more lines") {
		t.Fatal("F3 did not shorten the conversation with the prompt open")
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

	if got := last(); !strings.HasPrefix(got, startupFrames[1]+" working") {
		t.Fatalf("scrolling back hides the working spinner: %q", got)
	}
	m.rows[m.cursor].sess.Status = status.Idle
	if got := last(); strings.Contains(got, " working") || m.needsLoaderTick() {
		t.Fatalf("idle conversation retains the working spinner: %q", got)
	}
	m.rows[m.cursor].sess.Status = status.Working
	m.conversation.messages, m.conversation.dirty = nil, true
	if got := last(); !strings.HasPrefix(got, startupFrames[1]+" working") {
		t.Fatalf("empty conversation hides the spinner: %q", got)
	}
}

func TestConversationGroupsSequentialSameRoleMessages(t *testing.T) {
	c := conversationView{dirty: true, messages: []search.Message{
		{Role: "user", Text: "first question"},
		{Role: "user", Text: "second question"},
		{Role: "assistant", Text: "reply one"},
		{Role: "assistant", Text: "reply two"},
		{Role: "user", Text: "follow-up"},
	}}
	rows := c.wrapped(40)
	plain := ansi.Strip(strings.Join(rows, "\n"))
	if got := strings.Count(plain, "╭"); got != 3 {
		t.Fatalf("boxes = %d, want 3 (one per speaker run)", got)
	}
	for _, want := range []string{"first question", "second question", "reply one", "reply two", "follow-up"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q: %s", want, plain)
		}
	}
	if got := strings.Count(plain, "You"); got != 2 {
		t.Fatalf("You headings = %d, want 2", got)
	}
	if got := strings.Count(plain, "Assistant"); got != 1 {
		t.Fatalf("Assistant headings = %d, want 1", got)
	}
	for _, row := range rows {
		if textfmt.Width(row) > 40 {
			t.Fatalf("row exceeds 40 columns: %q", row)
		}
	}
}

func TestConversationUserAndAssistantBoxesUseDistinctColors(t *testing.T) {
	c := conversationView{dirty: true, messages: []search.Message{
		{Role: "user", Text: "hello"},
		{Role: "assistant", Text: "hi"},
	}}
	rows := c.wrapped(40)
	var youHead, assistantHead string
	for _, row := range rows {
		stripped := ansi.Strip(row)
		switch {
		case strings.Contains(stripped, "You"):
			if youHead == "" {
				youHead = row
			}
		case strings.Contains(stripped, "Assistant"):
			if assistantHead == "" {
				assistantHead = row
			}
		}
	}
	if youHead == "" || assistantHead == "" {
		t.Fatal("missing a speaker heading")
	}
	seqRe := regexp.MustCompile("\x1b\\[[0-9;]*m")
	youSeq := strings.Join(seqRe.FindAllString(youHead, -1), ",")
	assistantSeq := strings.Join(seqRe.FindAllString(assistantHead, -1), ",")
	if youSeq == "" || assistantSeq == "" {
		t.Fatal("headings carry no style sequences")
	}
	if youSeq == assistantSeq {
		t.Fatal("user and assistant boxes render in the same color")
	}
}

func TestCompactConversationShowsNewestGroupInFull(t *testing.T) {
	m := drainFleet(t)
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{key: conversationKey(sess.ID, sess.AgentSessionID), messages: []search.Message{
		{Role: "user", Text: "first\nsecond\nthird\nfourth\nburied detail\nsixth"},
		{Role: "assistant", Text: "alpha\nbeta\ngamma\ndelta\nvisible detail\nmore visible"},
	}})
	view := ansi.Strip(strings.Join(m.conversationRows(80, 40), "\n"))
	if !strings.Contains(view, "visible detail") {
		t.Fatalf("newest group is not full:\n%s", view)
	}
	if strings.Contains(view, "buried detail") {
		t.Fatalf("older group is not shortened:\n%s", view)
	}
	if !strings.Contains(view, "more lines") {
		t.Fatalf("shortened group hides its overflow marker:\n%s", view)
	}
}

func TestConversationHoverExpandsShortenedGroup(t *testing.T) {
	m := drainFleet(t)
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{key: conversationKey(sess.ID, sess.AgentSessionID), messages: []search.Message{
		{Role: "user", Text: "one\ntwo\nthree\nfour\nhover buried detail\nsixth"},
		{Role: "assistant", Text: "alpha\nbeta\ngamma\ndelta\nmiddle detail\nzeta"},
		{Role: "user", Text: "tail"},
	}})
	const width, height = 80, 40
	m.conversationLines(width, height)
	box := m.pane.box
	if !box.ok {
		t.Fatal("conversation did not record its pane box")
	}
	view := func() string {
		return ansi.Strip(strings.Join(m.conversationRows(width, height), "\n"))
	}
	if strings.Contains(view(), "hover buried detail") {
		t.Fatal("older group starts expanded")
	}
	if !m.updateConversationHover(box.x, box.y) {
		t.Fatal("hover over the first group changed nothing")
	}
	if m.conversation.hovered != 0 {
		t.Fatalf("hovered = %d, want the first group", m.conversation.hovered)
	}
	if !strings.Contains(view(), "hover buried detail") {
		t.Fatal("hover did not expand the group under the pointer")
	}
	lastStart := m.conversation.starts[len(m.conversation.starts)-1]
	m.updateConversationHover(box.x, box.y+lastStart)
	if m.conversation.hovered != -1 {
		t.Fatal("hovering the newest group kept an expansion it does not need")
	}
	if !m.updateConversationHover(box.x, box.y) || m.conversation.hovered != 0 {
		t.Fatal("re-hover did not expand the first group again")
	}
	m.updateConversationHover(box.x+box.width, box.y)
	if m.conversation.hovered != -1 {
		t.Fatal("leaving the pane kept the expansion")
	}
	if strings.Contains(view(), "hover buried detail") {
		t.Fatal("leaving the pane did not shorten the group again")
	}
}

func TestConversationScrollDropsHoverExpansion(t *testing.T) {
	m := drainFleet(t)
	sess, _ := m.selected()
	m.applyConversation(conversationMsg{key: conversationKey(sess.ID, sess.AgentSessionID), messages: []search.Message{
		{Role: "user", Text: "one\ntwo\nthree\nfour\nhover buried detail\nsixth"},
		{Role: "assistant", Text: strings.Repeat("middle detail\n", 30)},
		{Role: "user", Text: "tail"},
	}})
	width := m.previewPaneWidth()
	const height = 12
	m.conversationLines(width, height)
	box := m.pane.box
	// Scroll to the top so the first group is under the pointer.
	m.scrollConversation(-100)
	if !m.updateConversationHover(box.x, box.y) || m.conversation.hovered != 0 {
		t.Fatal("setup did not expand the first group")
	}
	m.scrollConversation(1)
	if m.conversation.hovered != -1 {
		t.Fatal("scroll kept the hovered expansion under a still pointer")
	}
}
