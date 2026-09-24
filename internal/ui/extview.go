package ui

// A screen of an extension's own: a card the board draws over the list, whose
// lines the extension supplies and whose keys resolve on a key map screen the
// extension named.
//
// The board keeps the frame, the key map and the way off. The view is asked
// for lines of styled text at the size the card has, and told each key with
// the action it stands for on its screen; it never sees the model, a Bubble
// Tea message or a style. Its screen always has a close action, which the
// board answers itself, so no view can be a screen with no way off it.

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
)

// ActionClose is the action that closes an extension's view. A screen whose
// extension binds none gets it on esc.
const ActionClose keymap.Action = "close"

// ExtensionView is what an extension draws on its screen. Every method is
// called on the board's event loop and must return promptly: a view loads
// off the loop and asks for a repaint through its handle.
type ExtensionView interface {
	Title() string
	// Render is the card's body at width cells by height rows. Rows past
	// height are not drawn, and each row is cut to width.
	Render(width, height int) [][]Span
	// Key is one press the board did not answer itself; true closes the
	// view.
	Key(ViewKey) bool
}

// ViewCloser is an ExtensionView told when the board closes it.
type ViewCloser interface {
	Closed(CloseReason)
}

// CloseReason is why the board closed a view.
type CloseReason string

const (
	// CloseDismissed is the screen's close action.
	CloseDismissed CloseReason = "dismissed"
	// CloseSubmitted is the view answering true to a submit.
	CloseSubmitted CloseReason = "submitted"
	// CloseReturned is the view answering true to any other press.
	CloseReturned CloseReason = "returned"
	// CloseHandle is the view's handle closing it.
	CloseHandle CloseReason = "handle"
	// CloseReplaced is another view opened over it.
	CloseReplaced CloseReason = "replaced"
)

// Span is a run of text in one tone.
type Span struct {
	Text string
	Tone Tone
	Bold bool
}

// ViewKey is a press as a view is told it.
type ViewKey struct {
	// Action is what the press stands for on the view's screen, or "" when
	// it is bound to nothing there.
	Action string
	// Key is the press's name, the spelling the key file uses.
	Key string
	// Text is what the press types, for a view with a field in it.
	Text string
	// Field is the field that had the keyboard, and Values every field's
	// value by ID, for a view with fields; both are empty otherwise.
	Field  string
	Values map[string]string
}

// ViewHandle is an opened view, as the extension that opened it holds it.
type ViewHandle struct {
	bridge *ExtensionBridge
	id     int
}

// Refresh asks the board to draw the view again.
func (h ViewHandle) Refresh() { h.bridge.post(extensionViewMsg{id: h.id}) }

// Close closes the view if it is still the one on screen.
func (h ViewHandle) Close() { h.bridge.post(extensionViewMsg{id: h.id, close: true}) }

// Open asks the board to show view on screen. It is shown only if the board
// is on its list, or on another extension view, when the request arrives.
func (b *ExtensionBridge) Open(owner, screen string, view ExtensionView) ViewHandle {
	b.mu.Lock()
	b.views++
	id := b.views
	b.mu.Unlock()
	b.post(extensionOpenMsg{id: id, owner: owner, screen: keymap.Context(screen), view: view})
	return ViewHandle{bridge: b, id: id}
}

// post delivers msg once the bridge is attached; before that it is dropped,
// because nothing can be on screen to act on.
func (b *ExtensionBridge) post(msg tea.Msg) {
	b.mu.Lock()
	send := b.send
	b.mu.Unlock()
	if send != nil {
		go send(msg)
	}
}

type extensionOpenMsg struct {
	id     int
	owner  string
	screen keymap.Context
	view   ExtensionView
}

type extensionViewMsg struct {
	id    int
	close bool
}

// openView is the extension view on screen.
type openView struct {
	id     int
	owner  string
	screen keymap.Context
	view   ExtensionView
	// fields are the inputs of a view with fields; see extfields.go.
	fields *viewFields
}

func (m *Model) updateExtensionView(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case extensionOpenMsg:
		m.openExtensionView(msg)
	case extensionViewMsg:
		if m.mode != modeExtensionView || m.extView.id != msg.id {
			return true
		}
		if msg.close {
			m.closeExtensionView(CloseHandle)
		}
	default:
		return false
	}
	return true
}

func (m *Model) openExtensionView(msg extensionOpenMsg) {
	if !m.extScreens[msg.screen] {
		m.errBar.text = msg.owner + ": " + string(msg.screen) + " is not a screen it declared keys for"
		return
	}
	onList := m.mode == modeList && !m.searching && !m.quick.active
	if !onList && m.mode != modeExtensionView {
		// The operator has moved on since the key that asked for this;
		// taking the screen from under them would lose what they are doing.
		logging.Info("extension view not opened: another screen is up", "extension", msg.owner, "screen", string(msg.screen))
		return
	}
	replaced := m.extView
	m.extView = openView{id: msg.id, owner: msg.owner, screen: msg.screen, view: msg.view}
	m.mode = modeExtensionView
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				m.failView(recovered)
			}
		}()
		m.syncViewFields()
	}()
	if replaced.view != nil {
		m.tellClosed(replaced, CloseReplaced)
	}
}

// closeExtensionView puts the list back and then tells the view why it was
// closed, so a view that opens another from Closed opens it over the list.
func (m *Model) closeExtensionView(reason CloseReason) {
	closed := m.extView
	m.dropExtensionView()
	m.tellClosed(closed, reason)
}

func (m *Model) dropExtensionView() {
	m.extView = openView{}
	m.mode = modeList
}

// tellClosed tells a view that implements ViewCloser it was closed. A Closed
// that panics is reported like a view that panics; the board is already off
// the view, so there is nothing more to close.
func (m *Model) tellClosed(closed openView, reason CloseReason) {
	closer, ok := closed.view.(ViewCloser)
	if !ok {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Warn("extension view panicked while closing", "extension", closed.owner, "panic", fmt.Sprint(recovered))
			m.errBar.text = fmt.Sprintf("%s: its view failed as it closed: %v", closed.owner, recovered)
		}
	}()
	closer.Closed(reason)
}

// failView closes a view that panicked, and says so. The view is not told
// it was closed: it is broken.
func (m *Model) failView(recovered any) {
	owner := m.extView.owner
	logging.Warn("extension view panicked", "extension", owner, "panic", fmt.Sprint(recovered))
	m.dropExtensionView()
	m.errBar.text = fmt.Sprintf("%s: its view failed and was closed: %v", owner, recovered)
}

func (m *Model) handleExtensionViewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	action, bound := m.action(m.extView.screen, msg)
	if bound && action == ActionClose {
		m.closeExtensionView(CloseDismissed)
		return m, nil
	}
	if m.extView.fields != nil {
		if handled, cmd := m.handleViewFieldKey(msg, action, bound); handled {
			return m, cmd
		}
	}
	key := ViewKey{Key: keyName(msg), Text: cleanText(msg.Key().Text)}
	if bound {
		key.Action = string(action)
	}
	m.tellView(key)
	return m, nil
}

// tellView hands a press to the view, with its fields' values when it has
// fields, and closes it when it asks.
func (m *Model) tellView(key ViewKey) {
	if fields := m.extView.fields; fields != nil {
		key.Field, key.Values = fields.order[fields.focus], fields.values()
	}
	closed := func() (closed bool) {
		defer func() {
			if recovered := recover(); recovered != nil {
				m.failView(recovered)
				closed = false
			}
		}()
		return m.extView.view.Key(key)
	}()
	if closed && m.mode == modeExtensionView {
		reason := CloseReturned
		if key.Action == string(ActionSubmit) {
			reason = CloseSubmitted
		}
		m.closeExtensionView(reason)
	}
}

func (m *Model) viewExtension() (frame string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			m.failView(recovered)
			frame = m.viewListFrame()
		}
	}()
	width := helpCardWidth(m.width)
	inner := cardInnerWidth(width)
	m.syncViewFields()
	hint := m.extensionViewHint()
	if m.extView.fields != nil {
		hint = append(m.fieldHint(), hint...)
	}
	room := m.height - 5 - lipgloss.Height(legendInline(hint, inner))
	if m.errBar.text != "" {
		room -= 2
	}
	room = max(room, 1)
	// The fields go under the view's own lines, which get what the fields
	// leave -- and a third of the card however many fields there are, so a
	// long form cannot push the view's own words off it.
	var fieldBlock []string
	var fieldStarts []int
	bodyRoom := room
	if m.extView.fields != nil {
		fieldBlock, fieldStarts = m.fieldLines(inner)
		bodyRoom = max(room-len(fieldBlock)-1, room/3)
	}
	rows := m.extView.view.Render(inner, bodyRoom)
	lines := make([]string, 0, room)
	styles := viewStyles()
	for _, row := range rows[:min(len(rows), bodyRoom)] {
		var b strings.Builder
		for _, span := range row {
			b.WriteString(styles[viewStyleKey{span.Tone, span.Bold}].Render(cleanText(span.Text)))
		}
		lines = append(lines, cellTruncate(b.String(), inner, "…"))
	}
	if len(fieldBlock) > 0 {
		if len(lines) > 0 && room-len(lines) > 1 {
			lines = append(lines, "")
		}
		lines = append(lines, m.fieldWindow(fieldBlock, fieldStarts, max(room-len(lines), 1))...)
	}
	return m.cardSized(width, badgeText(m.extView.view.Title()), strings.Join(lines, "\n"), hint)
}

// extensionViewHint is the view's screen's keys, from the key map, so a
// rebind shows up in the hint the moment it is made.
func (m *Model) extensionViewHint() [][2]string {
	var hint, closing [][2]string
	for _, binding := range m.km().Bindings(m.extView.screen) {
		if len(binding.Keys) == 0 {
			continue
		}
		pair := [2]string{keymap.Display(binding.Keys[0]), binding.Label}
		if binding.Action == ActionClose {
			closing = append(closing, pair)
			continue
		}
		hint = append(hint, pair)
	}
	return append(hint, closing...)
}

type viewStyleKey struct {
	tone Tone
	bold bool
}

// viewStyles is every tone, plain and bold, in the theme in force now.
func viewStyles() map[viewStyleKey]fastStyle {
	colors := map[Tone]lipgloss.Style{
		ToneMuted:  lipgloss.NewStyle().Foreground(colorText),
		ToneAccent: lipgloss.NewStyle().Foreground(colorAccent),
		ToneGood:   lipgloss.NewStyle().Foreground(statusColor(status.Finished)),
		ToneWarn:   lipgloss.NewStyle().Foreground(statusColor(status.Waiting)),
		ToneBad:    lipgloss.NewStyle().Foreground(statusColor(status.Errored)),
	}
	out := make(map[viewStyleKey]fastStyle, 2*len(colors))
	for tone, style := range colors {
		out[viewStyleKey{tone, false}] = newFastStyle(style)
		out[viewStyleKey{tone, true}] = newFastStyle(style.Bold(true))
	}
	return out
}

// viewScreens is every screen the extensions' keys name other than the list.
func viewScreens(uis []ExtensionUI) []keymap.Context {
	var out []keymap.Context
	for _, ui := range uis {
		for _, key := range ui.Keys {
			screen := keymap.Context(key.Screen)
			if screen == "" || screen == keymap.ContextList || slices.Contains(out, screen) {
				continue
			}
			out = append(out, screen)
		}
	}
	return out
}
