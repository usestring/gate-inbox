package ui

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/search"
)

func chainFixture(t testing.TB) *Model {
	t.Helper()
	m := buildModel(t)
	createSession(t, m, "chain-one", t.TempDir(), "")
	createSession(t, m, "chain-two", t.TempDir(), "")
	return m
}

func TestListPaneScrollChainsToAdjacentRow(t *testing.T) {
	m := chainFixture(t)
	m.conversation = nil
	m.selectSessionRow(t, "chain-one")
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected")
	}
	m.pane.forID = sess.ID
	m.pane.history = 20
	m.pane.mouse = false
	m.focusScroll = 0

	before := m.cursor
	m.keyScrollFocus(focusScrollDown)
	if m.cursor == before {
		t.Fatal("alt+down at the live bottom did not move to the next row")
	}
	if m.mode != modeList {
		t.Fatalf("mode = %v, want the list", m.mode)
	}

	sess, _ = m.selected()
	m.pane.forID = sess.ID
	m.pane.history = 20
	m.focusScroll = 20
	before = m.cursor
	m.keyScrollFocus(focusScrollUp)
	if m.cursor == before {
		t.Fatal("alt+up at the history top did not move to the previous row")
	}

	sess, _ = m.selected()
	m.pane.forID = sess.ID
	m.pane.history = 20
	m.focusScroll = 10
	before = m.cursor
	m.keyScrollFocus(focusScrollUp)
	if m.cursor != before {
		t.Fatal("alt+up mid-history moved the cursor instead of scrolling")
	}
	if m.focusScroll == 10 {
		t.Fatal("alt+up mid-history did not scroll")
	}
}

func TestFocusPaneScrollChainsOutToAdjacentRow(t *testing.T) {
	m := chainFixture(t)
	m.conversation = nil
	m.selectSessionRow(t, "chain-one")
	updated, _ := m.focusSelected()
	m = updated.(*Model)
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected")
	}
	m.pane.forID = sess.ID
	m.pane.history = 20
	m.pane.mouse = false
	m.focusScroll = 0

	m.keyScrollFocus(focusScrollDown)
	if m.mode != modeList {
		t.Fatalf("mode = %v, want the scroll past the bottom to leave focus", m.mode)
	}
	row, ok := m.selectedRow()
	if !ok || !row.isSession() || row.sess.Name != "chain-two" {
		t.Fatalf("landed on %+v, want chain-two", row)
	}

	m.selectSessionRow(t, "chain-two")
	updated, _ = m.focusSelected()
	m = updated.(*Model)
	sess, _ = m.selected()
	m.pane.forID = sess.ID
	m.pane.history = 20
	m.pane.mouse = false
	m.focusScroll = 20

	m.keyScrollFocus(focusScrollUp)
	if m.mode != modeList {
		t.Fatalf("mode = %v, want the scroll past the top to leave focus", m.mode)
	}
	row, ok = m.selectedRow()
	if !ok || !row.isSession() || row.sess.Name != "chain-one" {
		t.Fatalf("landed on %+v, want chain-one", row)
	}
}

func TestListConversationScrollChainsToAdjacentRow(t *testing.T) {
	longConversation := func(m *Model) {
		sess, _ := m.selected()
		m.applyConversation(conversationMsg{key: conversationKey(sess.ID, sess.AgentSessionID), messages: []search.Message{
			{Role: "user", Text: "My question"},
			{Role: "assistant", Text: strings.Repeat("Reply line\n", 60)},
		}})
		if m.conversation.compact {
			m.toggleConversation()
		}
	}

	m := chainFixture(t)
	m.selectSessionRow(t, "chain-one")
	if !m.showsConversation() {
		t.Skip("fixture is not showing a conversation")
	}
	longConversation(m)
	before := m.cursor
	m.keyScrollFocus(focusScrollDown)
	if m.cursor == before {
		t.Fatal("alt+down at the conversation bottom did not move to the next row")
	}

	m = chainFixture(t)
	m.selectSessionRow(t, "chain-two")
	if !m.showsConversation() {
		t.Skip("fixture is not showing a conversation")
	}
	longConversation(m)
	before = m.cursor
	m.keyScrollFocus(focusScrollTop)
	if m.cursor != before {
		t.Fatal("alt+home moved the cursor instead of scrolling to the top")
	}
	m.keyScrollFocus(focusScrollUp)
	if m.cursor == before {
		t.Fatal("alt+up at the conversation top did not move to the previous row")
	}
}
