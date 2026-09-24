package ui

// What a build's extensions add to the board's screens: keys on the list,
// badges on a session's row, and screens of their own (see extview.go).
//
// Nothing here hands an extension the model. A key is an action name the
// key map resolves like any other, and pressing it runs the extension's
// function off the event loop with the row it was pressed on. A badge is text
// and a tone that the extension pushes whenever it likes; the row renderer
// reads the last one pushed and never calls out. So a slow or broken
// extension can cost its own key and its own badge, and never the frame.

import (
	"fmt"
	"slices"
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
	Owner   string
	Keys    []ExtensionKey
	Filters []ExtensionFilter
}

// ExtensionKey is one action an extension adds to a screen.
type ExtensionKey struct {
	// Screen is the list when empty, or the name of a screen the extension
	// opens views on. The board's other screens take no extension keys.
	Screen string
	Action string
	Keys   []string
	Label  string
	// Run answers the key on the list. It is called off the event loop; an
	// error it returns is put on the status bar. On a view's screen the view
	// answers instead, and Run is not called.
	Run func(Press) error

	// filter is the list filter this key toggles, for a key made from one
	// of the ExtensionUI's Filters.
	filter *listFilter
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
	mu     sync.Mutex
	owners []string
	badges map[string]map[string][]Badge
	// headers, hidden and owned are what each owner has set on rows: see
	// extrows.go.
	headers map[string]map[string][]Span
	hidden  map[string]map[string]bool
	owned   map[string]map[string]bool
	// attention is each owner's claims on the queue: see extattention.go.
	attention map[string]map[string]Attention
	send      func(tea.Msg)
	pending   bool
	dirty     bool
	views     int
}

// NewExtensionBridge makes the bridge. owners fixes the order badges are
// drawn in: the order the build lists its extensions.
func NewExtensionBridge(owners []string) *ExtensionBridge {
	return &ExtensionBridge{
		owners:    append([]string(nil), owners...),
		badges:    map[string]map[string][]Badge{},
		headers:   map[string]map[string][]Span{},
		hidden:    map[string]map[string]bool{},
		owned:     map[string]map[string]bool{},
		attention: map[string]map[string]Attention{},
	}
}

// Attach starts delivering to the program, flushing whatever was set before.
func (b *ExtensionBridge) Attach(send func(tea.Msg)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.send = send
	if b.dirty {
		b.changedLocked()
	}
}

// changedLocked tells the loop the rows changed. One message in flight at a
// time: a burst of changes is one repaint, read in full when the loop gets
// to it.
func (b *ExtensionBridge) changedLocked() {
	b.dirty = true
	if b.send != nil && !b.pending {
		b.pending = true
		go b.send(extensionBadgesMsg{})
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
	b.changedLocked()
}

// Notify puts a line from owner on the status bar.
func (b *ExtensionBridge) Notify(owner, text string) {
	b.post(extensionNoticeMsg{owner: owner, text: badgeText(text)})
}

// rowMarks is everything the extensions have set on rows, merged across
// owners in build order.
type rowMarks struct {
	badges  map[string][]Badge
	headers map[string][][]Span
	hidden  map[string]bool
	owned   map[string]bool
	// attention is every owner's claims merged: see Attention.merge.
	attention map[string]Attention
}

// snapshot is every session's marks, owners in build order.
func (b *ExtensionBridge) snapshot() rowMarks {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending, b.dirty = false, false
	out := rowMarks{
		badges:    map[string][]Badge{},
		headers:   map[string][][]Span{},
		hidden:    map[string]bool{},
		owned:     map[string]bool{},
		attention: map[string]Attention{},
	}
	for _, owner := range b.owners {
		for sessionID, badges := range b.badges[owner] {
			out.badges[sessionID] = append(out.badges[sessionID], badges...)
		}
		for sessionID, header := range b.headers[owner] {
			out.headers[sessionID] = append(out.headers[sessionID], header)
		}
		for sessionID := range b.hidden[owner] {
			out.hidden[sessionID] = true
		}
		for sessionID := range b.owned[owner] {
			out.owned[sessionID] = true
		}
		for sessionID, claim := range b.attention[owner] {
			out.attention[sessionID] = out.attention[sessionID].merge(claim)
		}
	}
	return out
}

// badgeText is text an extension handed over, made safe to put in a row: one
// line, and nothing a terminal would read as a command.
func badgeText(text string) string {
	return strings.TrimSpace(cleanText(text))
}

// cleanText is text with every control character taken out, tabs included,
// so what an extension writes can move the cursor nowhere.
func cleanText(text string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, text)
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
// badges and views arrive on, and resolves the key map again with those keys
// in it. It is called before the program runs.
//
// A key naming one of the board's own screens other than the list is left
// out: those screens answer every key themselves. Every other screen named is
// a view screen, and one no extension gives a close action gets close on esc.
func (m *Model) InstallExtensions(uis []ExtensionUI, bridge *ExtensionBridge) {
	m.extBridge = bridge
	m.extUIs = nil
	m.extFilters = nil
	for _, ui := range uis {
		kept := ExtensionUI{Owner: ui.Owner}
		keys := append([]ExtensionKey(nil), ui.Keys...)
		for _, filter := range ui.Filters {
			listed := m.newListFilter(ui.Owner, filter)
			m.extFilters = append(m.extFilters, listed)
			keys = append(keys, ExtensionKey{Action: filter.Action, Keys: filter.Keys, Label: filter.Label, filter: listed})
		}
		for _, key := range keys {
			screen := keymap.Context(key.Screen)
			if screen != "" && screen != keymap.ContextList && slices.Contains(keymap.Contexts, screen) {
				logging.Warn("extension key names one of the board's own screens",
					"extension", ui.Owner, "screen", key.Screen, "action", key.Action)
				continue
			}
			kept.Keys = append(kept.Keys, key)
		}
		m.extUIs = append(m.extUIs, kept)
	}
	m.extScreens = map[keymap.Context]bool{}
	for _, screen := range viewScreens(m.extUIs) {
		m.extScreens[screen] = true
		if !m.declaresClose(screen) {
			for i := range m.extUIs {
				if m.hasScreen(i, screen) {
					m.extUIs[i].Keys = append(m.extUIs[i].Keys, ExtensionKey{
						Screen: string(screen), Action: string(ActionClose), Keys: []string{"esc"}, Label: "close"})
					break
				}
			}
		}
	}
	m.extKeys = map[keymap.Context]map[keymap.Action]extensionKey{}
	m.loadKeys()
	for _, ui := range m.extUIs {
		for _, key := range ui.Keys {
			ctx, action := keyScreen(key), keymap.Action(key.Action)
			if !m.ownsAction(ctx, action) {
				continue
			}
			if m.extKeys[ctx] == nil {
				m.extKeys[ctx] = map[keymap.Action]extensionKey{}
			}
			if _, taken := m.extKeys[ctx][action]; taken {
				continue
			}
			m.extKeys[ctx][action] = extensionKey{owner: ui.Owner, key: key}
		}
	}
}

func (m *Model) declaresClose(screen keymap.Context) bool {
	for _, ui := range m.extUIs {
		for _, key := range ui.Keys {
			if keyScreen(key) == screen && keymap.Action(key.Action) == ActionClose {
				return true
			}
		}
	}
	return false
}

func (m *Model) hasScreen(i int, screen keymap.Context) bool {
	for _, key := range m.extUIs[i].Keys {
		if keyScreen(key) == screen {
			return true
		}
	}
	return false
}

func keyScreen(key ExtensionKey) keymap.Context {
	if key.Screen == "" {
		return keymap.ContextList
	}
	return keymap.Context(key.Screen)
}

// extraBindings is every extension key as the key map takes it. A view
// screen's close is required: it is the way off that screen.
func (m *Model) extraBindings() []keymap.Binding {
	var out []keymap.Binding
	for _, ui := range m.extUIs {
		for _, key := range ui.Keys {
			ctx := keyScreen(key)
			out = append(out, keymap.Binding{
				Context:  ctx,
				Action:   keymap.Action(key.Action),
				Keys:     key.Keys,
				Label:    key.Label,
				Required: ctx != keymap.ContextList && keymap.Action(key.Action) == ActionClose,
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
	if ext.key.filter != nil {
		return m.toggleListFilter(ext.key.filter)
	}
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
	if m.updateExtensionView(msg) {
		return true
	}
	switch msg := msg.(type) {
	case extensionBadgesMsg:
		if m.extBridge != nil {
			m.applyRowMarks(m.extBridge.snapshot())
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

// extensionHelpSections is one key map section per extension and screen:
// its keys on the list titled with its id, and each of its view screens'
// keys titled with the id and the screen.
func (m *Model) extensionHelpSections() []helpSection {
	var out []helpSection
	for _, ui := range m.extUIs {
		screens := []keymap.Context{keymap.ContextList}
		for _, key := range ui.Keys {
			if ctx := keyScreen(key); !slices.Contains(screens, ctx) {
				screens = append(screens, ctx)
			}
		}
		for _, ctx := range screens {
			var rows []helpRow
			for _, key := range ui.Keys {
				action := keymap.Action(key.Action)
				if keyScreen(key) != ctx {
					continue
				}
				if ext, ok := m.extKeys[ctx][action]; ok && ext.owner == ui.Owner {
					rows = append(rows, bound(ctx, action, key.Label))
				}
			}
			if len(rows) == 0 {
				continue
			}
			title := ui.Owner
			if ctx != keymap.ContextList {
				title += " · " + string(ctx)
			}
			out = append(out, helpSection{title: title, rows: rows})
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
