package ui

// An extension's header as a row of its own.
//
// A header is a line an extension draws over a session to say what the
// session belongs to (see extrows.go). Most are captions, and a key pressed
// on one is pressed on the session. An extension that sets OpenHeader has a
// header that is a thing in its own right -- a charter over the session doing
// its work -- so the header is a row: the cursor lands on it, the open key on
// it opens what the extension says it heads, and every other key still acts
// on the session below, through selectedRow.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
)

// extHeader is one owner's header over a session.
type extHeader struct {
	owner string
	spans []Span
}

// headOpener is the owner's OpenHeader, or nil when its headers are lines.
func (m *Model) headOpener(owner string) func(Press) error {
	for _, ui := range m.extUIs {
		if ui.Owner == owner {
			return ui.OpenHeader
		}
	}
	return nil
}

// headsRows is whether the owner's headers are rows: it opens them, or has
// a page behind them.
func (m *Model) headsRows(owner string) bool {
	return m.headOpener(owner) != nil || m.headPaneOf(owner) != nil
}

// headRows are the header rows over a session, owners in build order.
func (m *Model) headRows(sess store.Session, depth int) []treeRow {
	var rows []treeRow
	for _, header := range m.extHeaders[sess.ID] {
		if m.headsRows(header.owner) {
			rows = append(rows, treeRow{sess: sess, depth: depth, head: header.owner})
		}
	}
	return rows
}

// headKeys is which sessions carry a header row, and whose. The tree holds
// those rows, so a change to it rebuilds the tree; a header whose text alone
// changed repaints.
func (m *Model) headKeys(headers map[string][]extHeader) map[string]bool {
	keys := map[string]bool{}
	for sessionID, list := range headers {
		for _, header := range list {
			if m.headsRows(header.owner) {
				keys[header.owner+"\x00"+sessionID] = true
			}
		}
	}
	return keys
}

// headSpans is the text of a header row.
func (m *Model) headSpans(entry treeRow) []Span {
	for _, header := range m.extHeaders[entry.sess.ID] {
		if header.owner == entry.head {
			return header.spans
		}
	}
	return nil
}

// renderHeadEntry draws a header row at its session's guides, as the line
// over the session was drawn before it became a row.
func (m *Model) renderHeadEntry(entry treeRow, selected bool, width, index int, pad, bg string) string {
	target := index
	for target < len(m.rows)-1 && m.rows[target].isHead() {
		target++
	}
	styles := viewStyles()
	var b strings.Builder
	for _, span := range m.headSpans(entry) {
		bold := span.Bold || selected
		b.WriteString(styles[viewStyleKey{span.Tone, bold}].Render(span.Text))
	}
	line := textfmt.TruncateWidth(pad+m.treeGuidesAbove(target)+b.String(), width, "…")
	return paint(line, width, bg)
}

// headRowAction answers the open keys on a header row by opening what the
// extension says the header is. Every other key falls through to the list,
// which acts on the session the header is over.
func (m *Model) headRowAction(action keymap.Action, bound bool) (tea.Model, tea.Cmd, bool) {
	entry, ok := m.cursorRow()
	if !ok || !entry.isHead() || !bound {
		return m, nil, false
	}
	switch action {
	case keymap.Open, keymap.StepIn:
	default:
		return m, nil, false
	}
	// A header with a page is focused the way a session is.
	if m.focusHeadPane() {
		return m, nil, true
	}
	open := m.headOpener(entry.head)
	if open == nil {
		return m, nil, false
	}
	owner, press := entry.head, Press{SessionID: entry.sess.ID, Group: entry.sess.Group}
	return m, func() (msg tea.Msg) {
		defer func() {
			if recovered := recover(); recovered != nil {
				msg = extensionKeyDoneMsg{owner: owner, action: "open_header", err: fmt.Errorf("panicked: %v", recovered)}
			}
		}()
		return extensionKeyDoneMsg{owner: owner, action: "open_header", err: open(press)}
	}, true
}
