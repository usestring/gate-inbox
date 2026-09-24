package ui

// What a build's extensions add to the board's screens: keys on the list,
// and badges on a session's row.
//
// Nothing here hands an extension the model. A key is an action name the
// key map resolves like any other, and pressing it runs the extension's
// function off the event loop with the row it was pressed on. A badge is text
// and a tone that the extension pushes whenever it likes; the row renderer
// reads the last one pushed and never calls out. So a slow or broken
// extension can cost its own key and its own badge, and never the frame.

import (
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
)

// ExtensionUI is one extension's contribution to the board's screens.
type ExtensionUI struct {
	// Owner is the extension's id. It titles the extension's section of the
	// key map and prefixes anything its keys report.
	Owner string
	Keys  []ExtensionKey
}

// ExtensionKey is one action an extension adds to the list.
type ExtensionKey struct {
	Action string
	Keys   []string
	Label  string
	// Run answers the key. It is called off the event loop; an error it
	// returns is put on the status bar.
	Run func(Press) error
}

// Press is the row a key was pressed on.
type Press struct {
	// SessionID is the session under the cursor, or "" on a group row.
	SessionID string
	// Group is that session's group, or the group row's own path.
	Group string
}

// Tone is how a badge is tinted: the board's own colours, by meaning.
type Tone int

const (
	ToneMuted Tone = iota
	ToneAccent
	ToneGood
	ToneWarn
	ToneBad
)

// Badge is one mark an extension puts on a session's row.
type Badge struct {
	Text string
	// Short stands in for Text where the row has no room for it. A badge
	// with neither room nor a Short is left off rather than cut.
	Short string
	Tone  Tone
}

// ExtensionBridge carries what extensions push, from whatever goroutine they
// push it on, to the event loop. Badges set before the program runs are held
// and delivered once Attach is called.
type ExtensionBridge struct {
	mu      sync.Mutex
	owners  []string
	badges  map[string]map[string][]Badge
	send    func(tea.Msg)
	pending bool
}

// NewExtensionBridge makes the bridge. owners fixes the order badges are
// drawn in: the order the build lists its extensions.
func NewExtensionBridge(owners []string) *ExtensionBridge {
	return &ExtensionBridge{owners: append([]string(nil), owners...), badges: map[string]map[string][]Badge{}}
}

// Attach starts delivering to the program, flushing whatever was set before.
func (b *ExtensionBridge) Attach(send func(tea.Msg)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.send = send
	if len(b.badges) > 0 && !b.pending {
		b.pending = true
		go send(extensionBadgesMsg{})
	}
}

// Decorate replaces owner's badges on one session's row. No badges clears
// them.
func (b *ExtensionBridge) Decorate(owner, sessionID string, badges []Badge) {
	clean := make([]Badge, 0, len(badges))
	for _, badge := range badges {
		badge.Text, badge.Short = badgeText(badge.Text), badgeText(badge.Short)
		if badge.Text == "" {
			continue
		}
		clean = append(clean, badge)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(clean) == 0 {
		delete(b.badges[owner], sessionID)
	} else {
		if b.badges[owner] == nil {
			b.badges[owner] = map[string][]Badge{}
		}
		b.badges[owner][sessionID] = clean
	}
	// One message in flight at a time: a burst of badges is one repaint,
	// read in full when the loop gets to it.
	if b.send != nil && !b.pending {
		b.pending = true
		go b.send(extensionBadgesMsg{})
	}
}

// Notify puts a line from owner on the status bar.
func (b *ExtensionBridge) Notify(owner, text string) {
	b.mu.Lock()
	send := b.send
	b.mu.Unlock()
	if send != nil {
		go send(extensionNoticeMsg{owner: owner, text: badgeText(text)})
	}
}

// snapshot is every session's badges, owners in build order.
func (b *ExtensionBridge) snapshot() map[string][]Badge {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = false
	out := map[string][]Badge{}
	for _, owner := range b.owners {
		for sessionID, badges := range b.badges[owner] {
			out[sessionID] = append(out[sessionID], badges...)
		}
	}
	return out
}

// badgeText is text an extension handed over, made safe to put in a row: one
// line, and nothing a terminal would read as a command.
func badgeText(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, text))
}

type extensionBadgesMsg struct{}

type extensionNoticeMsg struct{ owner, text string }

type extensionKeyDoneMsg struct {
	owner, action string
	err           error
}

// extensionKey is a key as the model dispatches it: its owner and its run.
type extensionKey struct {
	owner string
	key   ExtensionKey
}

// InstallExtensions gives the model the extensions' keys and the bridge their
// badges arrive on, and resolves the key map again with those keys in it. It
// is called before the program runs.
func (m *Model) InstallExtensions(uis []ExtensionUI, bridge *ExtensionBridge) {
	m.extUIs = uis
	m.extBridge = bridge
	m.extKeys = map[keymap.Context]map[keymap.Action]extensionKey{}
	m.loadKeys()
	for _, ui := range uis {
		for _, key := range ui.Keys {
			action := keymap.Action(key.Action)
			if !m.ownsAction(keymap.ContextList, action) {
				continue
			}
			if m.extKeys[keymap.ContextList] == nil {
				m.extKeys[keymap.ContextList] = map[keymap.Action]extensionKey{}
			}
			if _, taken := m.extKeys[keymap.ContextList][action]; taken {
				continue
			}
			m.extKeys[keymap.ContextList][action] = extensionKey{owner: ui.Owner, key: key}
		}
	}
}

// extraBindings is every extension key as the key map takes it.
func (m *Model) extraBindings() []keymap.Binding {
	var out []keymap.Binding
	for _, ui := range m.extUIs {
		for _, key := range ui.Keys {
			out = append(out, keymap.Binding{
				Context: keymap.ContextList,
				Action:  keymap.Action(key.Action),
				Keys:    key.Keys,
				Label:   key.Label,
			})
		}
	}
	return out
}

// ownsAction reports whether an action on a screen is one the catalog does
// not define: an extension's, once the key map accepted it.
func (m *Model) ownsAction(ctx keymap.Context, action keymap.Action) bool {
	for _, binding := range keymap.Catalog {
		if binding.Context == ctx && binding.Action == action {
			return false
		}
	}
	for _, binding := range m.km().Bindings(ctx) {
		if binding.Action == action {
			return true
		}
	}
	return false
}

// runExtensionKey answers an extension's key off the event loop, with the row
// the cursor is on.
func (m *Model) runExtensionKey(ext extensionKey) tea.Cmd {
	var press Press
	if entry, ok := m.cursorRow(); ok {
		if entry.isGroup {
			press.Group = entry.group
		} else if entry.isSession() {
			press.SessionID, press.Group = entry.sess.ID, entry.sess.Group
		}
	}
	run := ext.key.Run
	owner, action := ext.owner, ext.key.Action
	if run == nil {
		return nil
	}
	return func() (msg tea.Msg) {
		defer func() {
			if recovered := recover(); recovered != nil {
				msg = extensionKeyDoneMsg{owner: owner, action: action, err: fmt.Errorf("panicked: %v", recovered)}
			}
		}()
		return extensionKeyDoneMsg{owner: owner, action: action, err: run(press)}
	}
}

func (m *Model) updateExtension(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case extensionBadgesMsg:
		if m.extBridge != nil {
			m.extBadges = m.extBridge.snapshot()
		}
	case extensionNoticeMsg:
		if msg.text != "" {
			m.reportDone(msg.text)
		}
	case extensionKeyDoneMsg:
		if msg.err != nil {
			logging.Warn("extension key failed", "extension", msg.owner, "action", msg.action, logging.Err(msg.err))
			m.errBar.text = msg.owner + ": " + msg.err.Error()
		}
	default:
		return false
	}
	return true
}

// extensionHelpSections is one key map section per extension with keys on
// the list, titled with its id.
func (m *Model) extensionHelpSections() []helpSection {
	var out []helpSection
	for _, ui := range m.extUIs {
		var rows []helpRow
		for _, key := range ui.Keys {
			action := keymap.Action(key.Action)
			if ext, ok := m.extKeys[keymap.ContextList][action]; ok && ext.owner == ui.Owner {
				rows = append(rows, listRow(action, key.Label))
			}
		}
		if len(rows) > 0 {
			out = append(out, helpSection{title: ui.Owner, rows: rows})
		}
	}
	return out
}

// extensionBadges is the row's extension badges that fit in room cells, each
// led by a space. A badge that does not fit falls back to its short form, and
// the first that fits in neither ends the run, so the badges that are drawn
// are always the first ones in order.
func (m *Model) extensionBadges(sessionID string, room int) string {
	badges := m.extBadges[sessionID]
	if len(badges) == 0 {
		return ""
	}
	var b strings.Builder
	for _, badge := range badges {
		text := badge.Text
		if cellWidth(text)+1 > room {
			text = badge.Short
		}
		if text == "" || cellWidth(text)+1 > room {
			break
		}
		room -= cellWidth(text) + 1
		b.WriteString(" " + badgeTone(badge.Tone, text))
	}
	return b.String()
}

func badgeTone(tone Tone, text string) string {
	// Rendered without the memo caches: those are keyed by bounded text, and
	// an extension's badge is whatever it says.
	switch tone {
	case ToneAccent:
		return accentStyle.Render(text)
	case ToneGood:
		return tinted(statusColor(status.Finished)).Render(text)
	case ToneWarn:
		return tinted(statusColor(status.Waiting)).Render(text)
	case ToneBad:
		return tinted(statusColor(status.Errored)).Render(text)
	}
	return subtleStyle.Render(text)
}
