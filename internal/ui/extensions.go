package ui

// What a build's extensions add to the board's screens: keys on the list,
// badges on a session's row, screens of their own (see extview.go), and a
// warning to confirm before a session opens.
//
// Nothing here hands an extension the model. A key is an action name the
// key map resolves like any other, and pressing it runs the extension's
// function off the event loop with the row it was pressed on. A badge is text
// and a tone that the extension pushes whenever it likes; the row renderer
// reads the last one pushed and never calls out. So a slow or broken
// extension can cost its own key and its own badge, and never the frame.

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/usestring/gate-inbox/extension"
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
	// Aliases are action names the operator's key file may still carry
	// for this key from before the extension took it over: an override
	// under one applies here.
	Aliases []string

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
	// Rungs are the badge's renditions, widest first: the row draws the
	// first that fits, and leaves the badge off rather than cut one that
	// does not. Each span keeps its own tone.
	Rungs [][]Span
	// Text, Short and Tone are the shorthand for a badge of one tone: when
	// Rungs is empty, its rungs are Text and then Short, both in Tone.
	Text  string
	Short string
	Tone  Tone
	// AfterName asks for the badge to be drawn right after the session's
	// name rather than in the extension-badge slot, room permitting.
	AfterName bool
	// widths is each rung's width, measured once when the badge is set.
	widths []int
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
	// warn is each owner's warning before a session opens.
	warn    map[string]map[string]string
	send    func(tea.Msg)
	pending bool
	dirty   bool
	views   int
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
		warn:      map[string]map[string]string{},
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
		if rungs := badgeRungs(badge); len(rungs) > 0 {
			clean = append(clean, Badge{Rungs: rungs, AfterName: badge.AfterName, widths: rungWidths(rungs)})
		}
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

// ConfirmOpen sets owner's warning for opening one session. An empty warning
// clears it. The model reads warnings when an open key is pressed, so setting
// one repaints nothing.
func (b *ExtensionBridge) ConfirmOpen(owner, sessionID, warning string) {
	warning = badgeText(warning)
	b.mu.Lock()
	defer b.mu.Unlock()
	if warning == "" {
		delete(b.warn[owner], sessionID)
		return
	}
	if b.warn[owner] == nil {
		b.warn[owner] = map[string]string{}
	}
	b.warn[owner][sessionID] = warning
}

// openWarning is every warning set for opening a session, owners in build
// order, or "" when none is.
func (b *ExtensionBridge) openWarning(sessionID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var warnings []string
	for _, owner := range b.owners {
		if warning := b.warn[owner][sessionID]; warning != "" {
			warnings = append(warnings, warning)
		}
	}
	return strings.Join(warnings, "; ")
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

// badgeRungs is a badge's renditions made safe to put in a row, the
// shorthand spelled out as rungs. A shorthand badge with no Text has none,
// and neither has a rung that cleans down to nothing.
func badgeRungs(badge Badge) [][]Span {
	rungs := badge.Rungs
	if len(rungs) == 0 {
		if badge.Text == "" {
			return nil
		}
		rungs = [][]Span{{{Text: badge.Text, Tone: badge.Tone}}}
		if badge.Short != "" {
			rungs = append(rungs, []Span{{Text: badge.Short, Tone: badge.Tone}})
		}
	}
	out := make([][]Span, 0, len(rungs))
	for _, rung := range rungs {
		clean := make([]Span, 0, len(rung))
		for _, span := range rung {
			if span.Text = cleanText(span.Text); span.Text != "" {
				clean = append(clean, span)
			}
		}
		// Only the rung's ends are trimmed: the spaces between its spans
		// are the extension's.
		for len(clean) > 0 {
			if clean[0].Text = strings.TrimLeft(clean[0].Text, " "); clean[0].Text != "" {
				break
			}
			clean = clean[1:]
		}
		for len(clean) > 0 {
			last := &clean[len(clean)-1]
			if last.Text = strings.TrimRight(last.Text, " "); last.Text != "" {
				break
			}
			clean = clean[:len(clean)-1]
		}
		if len(clean) > 0 {
			out = append(out, clean)
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
			keys = append(keys, ExtensionKey{Action: filter.Action, Keys: filter.Keys, Label: filter.Label,
				Aliases: filter.Aliases, filter: listed})
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

// extraAliases is every old action name an extension key answers to.
func (m *Model) extraAliases() []keymap.Alias {
	var out []keymap.Alias
	for _, ui := range m.extUIs {
		for _, key := range ui.Keys {
			for _, from := range key.Aliases {
				out = append(out, keymap.Alias{Context: keyScreen(key), From: keymap.Action(from), To: keymap.Action(key.Action)})
			}
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

// holdOpen reports whether an open key on the selected session should wait
// for a second press. The first press on a session an extension warns about
// shows the warning and holds; the next open press on the same session goes
// through. dropOpenHold ends the hold on every other key.
func (m *Model) holdOpen(action keymap.Action) bool {
	sess, ok := m.selected()
	if !ok || m.extBridge == nil {
		m.openHeld = ""
		return false
	}
	warning := m.extBridge.openWarning(sess.ID)
	if warning == "" || m.openHeld == sess.ID {
		m.dropOpenHold()
		return false
	}
	m.openHeld = sess.ID
	m.errBar.text = warning + " — " + m.cap(keymap.ContextList, action) + " again to open, esc to cancel"
	m.openHeldText = m.errBar.text
	return true
}

// dropOpenHold forgets a pending open, and takes its warning off the status
// bar if nothing has replaced it there.
func (m *Model) dropOpenHold() {
	if m.openHeld == "" {
		return
	}
	if m.errBar.text == m.openHeldText {
		m.errBar.text = ""
	}
	m.openHeld, m.openHeldText = "", ""
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

// extensionBadges is the row's extension badges: those drawn right after
// the name, fitted into nameRoom cells, and those drawn in the slot at the
// end of the row, fitted into slotRoom. shared says the two are on one line,
// so what the name's badges take comes out of the slot's room too.
//
// The badges placed after the name are fitted first, in build order, and the
// first with no rung that fits there sends itself and the rest of them to the
// slot. The slot then takes the other badges and those, still in build order:
// each is drawn as its widest rung that fits, and the first with no rung that
// fits ends the run, so the badges that are drawn are always the first ones.
// Each drawn badge is led by a space.
func (m *Model) extensionBadges(sessionID string, nameRoom, slotRoom int, shared bool) (afterName, slot string) {
	badges := m.extBadges[sessionID]
	if len(badges) == 0 {
		return "", ""
	}
	var after, end strings.Builder
	placed := make([]bool, len(badges))
	for i, badge := range badges {
		if !badge.AfterName {
			continue
		}
		used, ok := drawBadge(&after, badge, nameRoom)
		if !ok {
			break
		}
		placed[i] = true
		nameRoom -= used
		if shared {
			slotRoom -= used
		}
	}
	for i, badge := range badges {
		if placed[i] {
			continue
		}
		used, ok := drawBadge(&end, badge, slotRoom)
		if !ok {
			break
		}
		slotRoom -= used
	}
	return after.String(), end.String()
}

// drawBadge writes a space and badge's widest rung that fits in room cells,
// and says how many cells that took; a badge with no rung that fits writes
// nothing.
func drawBadge(b *strings.Builder, badge Badge, room int) (int, bool) {
	fit := -1
	for i := range badge.Rungs {
		if badge.widths[i]+1 <= room {
			fit = i
			break
		}
	}
	if fit < 0 {
		return 0, false
	}
	b.WriteString(" ")
	for _, span := range badge.Rungs[fit] {
		if span.Bold {
			b.WriteString(lipgloss.NewStyle().Foreground(badgeColor(span.Tone)).Bold(true).Render(span.Text))
			continue
		}
		b.WriteString(badgeTone(span.Tone, span.Text))
	}
	return badge.widths[fit] + 1, true
}

// rungWidths is each rung's width as the extension API measures a Line, so
// an extension sizing its rungs and the row fitting them agree.
func rungWidths(rungs [][]Span) []int {
	out := make([]int, len(rungs))
	for i, rung := range rungs {
		line := make(extension.Line, len(rung))
		for j, span := range rung {
			line[j].Text = span.Text
		}
		out[i] = line.Width()
	}
	return out
}

func badgeColor(tone Tone) color.Color {
	switch tone {
	case ToneAccent:
		return colorAccent
	case ToneGood:
		return statusColor(status.Finished)
	case ToneWarn:
		return statusColor(status.Waiting)
	case ToneBad:
		return statusColor(status.Errored)
	}
	return colorSubtle
}

func badgeTone(tone Tone, text string) string {
	// Rendered without the memo caches: those are keyed by bounded text, and
	// an extension's badge is whatever it says.
	switch tone {
	case ToneAccent:
		return accentStyle.Render(text)
	case ToneMuted:
		return subtleStyle.Render(text)
	}
	return tinted(badgeColor(tone)).Render(text)
}
