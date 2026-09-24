package ui

// Fields on an extension's view: text the operator types into boxes the
// board draws, with the board's own inputs.
//
// A view that has fields names them, and the board keeps one input per field
// for as long as the view is open: the cursor, the selection, paste, line
// breaks in a multi-line box, and which field has the keyboard are all the
// board's. The view never sees a keystroke that was typing; it is told the
// values when the operator submits, and alongside every other key it is
// told, so a view can act on what has been typed without keeping a copy.

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
)

// ActionSubmit is the action that hands a view's field values back to it.
// A screen that binds no submit gets it on ctrl+s; enter submits from a
// one-line field or a choice either way.
const ActionSubmit keymap.Action = "submit"

// ViewField is one field on an extension's view.
type ViewField struct {
	// ID names the field in the values handed back. Fields are matched by
	// ID from one call of Fields to the next.
	ID    string
	Label string
	// Value is what the field holds when the board first sees it, and what
	// it is set to whenever the view offers a different Value than it last
	// did. Otherwise the operator's typing stands.
	Value       string
	Placeholder string
	// Multiline makes the field a box that takes line breaks: enter is a new
	// line there, and submit is ctrl+s or the screen's own submit key.
	Multiline bool
	// Height is the rows a multi-line box shows; zero is three.
	Height int
	// Limit is the most characters the field takes; zero is the board's
	// default for its kind.
	Limit int
	// Choices makes the field a picker over these values, moved with left
	// and right, instead of a field that is typed into.
	Choices []string
}

// ExtensionForm is an extension view with fields.
type ExtensionForm interface {
	ExtensionView
	// Fields is called on the event loop each time the view is drawn.
	Fields() []ViewField
}

const (
	fieldLineLimit     = 400
	fieldBoxLimit      = 8000
	fieldDefaultHeight = 3
	fieldLabelMax      = 14
)

// viewFields is the board's side of a view's fields: an input per field, in
// the view's order, and which one has the keyboard.
type viewFields struct {
	order []string
	byID  map[string]*viewField
	focus int
}

type viewField struct {
	spec    ViewField
	offered string
	line    textinput.Model
	box     textarea.Model
	choice  int
	on      bool
}

func newViewField(spec ViewField) *viewField {
	f := &viewField{}
	switch {
	case len(spec.Choices) > 0:
	case spec.Multiline:
		f.box = textarea.New()
		f.box.ShowLineNumbers = false
		f.box.SetPromptFunc(2, func(textarea.PromptInfo) string { return "  " })
		styles := f.box.Styles()
		styles.Focused.CursorLine = lipgloss.NewStyle()
		f.box.SetStyles(styles)
	default:
		f.line = textinput.New()
	}
	f.apply(spec, true)
	return f
}

// apply takes what the view says about the field now. The value is set only
// when it is new or the view offers a different one than before.
func (f *viewField) apply(spec ViewField, first bool) {
	spec.Value = cleanFieldText(spec.Value, spec.Multiline)
	reset := first || spec.Value != f.offered
	f.spec, f.offered = spec, spec.Value
	switch {
	case len(spec.Choices) > 0:
		if reset {
			f.choice = 0
			for i, choice := range spec.Choices {
				if choice == spec.Value {
					f.choice = i
				}
			}
		}
		f.choice = min(f.choice, len(spec.Choices)-1)
	case spec.Multiline:
		f.box.Placeholder = cleanText(spec.Placeholder)
		f.box.CharLimit = limitOr(spec.Limit, fieldBoxLimit)
		f.box.SetHeight(heightOr(spec.Height))
		if reset {
			f.box.SetValue(spec.Value)
		}
	default:
		f.line.Placeholder = cleanText(spec.Placeholder)
		f.line.CharLimit = limitOr(spec.Limit, fieldLineLimit)
		if reset {
			f.line.SetValue(spec.Value)
		}
	}
}

func limitOr(limit, fallback int) int {
	if limit > 0 {
		return limit
	}
	return fallback
}

func heightOr(height int) int {
	if height > 0 {
		return height
	}
	return fieldDefaultHeight
}

// cleanFieldText is a value an extension offered, made safe to put in an
// input: line breaks kept only for a box, and nothing else a terminal would
// act on.
func cleanFieldText(text string, multiline bool) string {
	if !multiline {
		return cleanText(text)
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = cleanText(line)
	}
	return strings.Join(lines, "\n")
}

func (f *viewField) value() string {
	switch {
	case len(f.spec.Choices) > 0:
		return f.spec.Choices[f.choice]
	case f.spec.Multiline:
		return f.box.Value()
	}
	return f.line.Value()
}

func (f *viewField) typed() bool { return len(f.spec.Choices) == 0 }

func fieldKind(spec ViewField) int {
	switch {
	case len(spec.Choices) > 0:
		return 2
	case spec.Multiline:
		return 1
	}
	return 0
}

func (f *viewField) setFocus(on bool) tea.Cmd {
	if f.on == on {
		return nil
	}
	f.on = on
	switch {
	case !f.typed():
		return nil
	case f.spec.Multiline && on:
		return f.box.Focus()
	case f.spec.Multiline:
		f.box.Blur()
	case on:
		return f.line.Focus()
	default:
		f.line.Blur()
	}
	return nil
}

func (f *viewField) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	if f.spec.Multiline {
		f.box, cmd = f.box.Update(msg)
	} else {
		f.line, cmd = f.line.Update(msg)
	}
	return cmd
}

// syncViewFields reads the view's fields and brings the inputs in line with
// them. A view without fields has none; one whose fields all went away keeps
// none.
func (m *Model) syncViewFields() {
	form, ok := m.extView.view.(ExtensionForm)
	if !ok {
		m.extView.fields = nil
		return
	}
	specs := form.Fields()
	fields := m.extView.fields
	if fields == nil {
		fields = &viewFields{byID: map[string]*viewField{}}
	}
	focused := ""
	if fields.focus < len(fields.order) {
		focused = fields.order[fields.focus]
	}
	byID := make(map[string]*viewField, len(specs))
	order := make([]string, 0, len(specs))
	for _, spec := range specs {
		if spec.ID == "" || byID[spec.ID] != nil {
			continue
		}
		if len(spec.Choices) > 0 {
			spec.Choices = append([]string(nil), spec.Choices...)
		}
		// A field that changed kind is a new input: a line, a box and a
		// picker keep nothing in common.
		if f := fields.byID[spec.ID]; f != nil && fieldKind(f.spec) == fieldKind(spec) {
			f.apply(spec, false)
			byID[spec.ID] = f
		} else {
			byID[spec.ID] = newViewField(spec)
		}
		order = append(order, spec.ID)
	}
	fields.order, fields.byID, fields.focus = order, byID, 0
	for i, id := range order {
		if id == focused {
			fields.focus = i
		}
	}
	if len(order) == 0 {
		m.extView.fields = nil
		return
	}
	m.extView.fields = fields
	for i, id := range order {
		byID[id].setFocus(i == fields.focus)
	}
}

func (v *viewFields) focused() *viewField { return v.byID[v.order[v.focus]] }

func (v *viewFields) move(delta int) tea.Cmd {
	v.focused().setFocus(false)
	v.focus = (v.focus + delta + len(v.order)) % len(v.order)
	return v.focused().setFocus(true)
}

func (v *viewFields) values() map[string]string {
	out := make(map[string]string, len(v.order))
	for _, id := range v.order {
		out[id] = v.byID[id].value()
	}
	return out
}

// handleViewFieldKey answers a press on a view with fields. It reports
// false for a press the view itself should be told.
func (m *Model) handleViewFieldKey(msg tea.KeyPressMsg, action keymap.Action, bound bool) (bool, tea.Cmd) {
	fields := m.extView.fields
	field := fields.focused()
	key := msg.String()
	switch {
	case key == "tab":
		return true, fields.move(1)
	case key == "shift+tab":
		return true, fields.move(-1)
	case bound && action == ActionSubmit,
		key == "ctrl+s" && !m.screenBinds(ActionSubmit),
		key == "enter" && !field.spec.Multiline:
		m.tellView(ViewKey{Action: string(ActionSubmit), Key: keyName(msg)})
		return true, nil
	case !field.typed():
		if key == "left" || key == "right" {
			step := 1
			if key == "left" {
				step = -1
			}
			field.choice = (field.choice + step + len(field.spec.Choices)) % len(field.spec.Choices)
			return true, nil
		}
		return false, nil
	}
	// A typed field takes every other key, except one the screen binds with
	// ctrl or alt held: those are the view's commands, and nothing is typed
	// with them.
	if bound && msg.Key().Mod&(tea.ModCtrl|tea.ModAlt) != 0 {
		return false, nil
	}
	return true, field.update(msg)
}

// screenBinds reports whether the view's screen has a key for action.
func (m *Model) screenBinds(action keymap.Action) bool {
	for _, binding := range m.km().Bindings(m.extView.screen) {
		if binding.Action == action && len(binding.Keys) > 0 {
			return true
		}
	}
	return false
}

// pasteIntoView gives a paste to the view's focused field, when it types.
func (m *Model) pasteIntoView(msg tea.PasteMsg) tea.Cmd {
	fields := m.extView.fields
	if fields == nil || !fields.focused().typed() {
		return nil
	}
	return fields.focused().update(msg)
}

// fieldLines draws the fields at inner cells, and reports the line each
// field starts on so the window can keep the focused one in view.
func (m *Model) fieldLines(inner int) (lines []string, starts []int) {
	fields := m.extView.fields
	labelWidth := 0
	for _, id := range fields.order {
		if f := fields.byID[id]; !f.spec.Multiline {
			labelWidth = max(labelWidth, cellWidth(cleanText(f.spec.Label)))
		}
	}
	labelWidth = min(labelWidth, fieldLabelMax)
	valueWidth := max(inner-formMarkerWidth-labelWidth-1, 8)
	for i, id := range fields.order {
		f := fields.byID[id]
		starts = append(starts, len(lines))
		marker := "  "
		labelStyle := subtleStyle
		if i == fields.focus {
			marker, labelStyle = accentStyle.Render("❯ "), valueStyle
		}
		label := cellTruncate(cleanText(f.spec.Label), fieldLabelMax, "…")
		switch {
		case f.spec.Multiline:
			f.box.SetWidth(max(inner-formMarkerWidth, 8))
			lines = append(lines, marker+labelStyle.Render(label))
			for _, row := range splitLines(f.box.View()) {
				lines = append(lines, spaces(formMarkerWidth)+row)
			}
		case !f.typed():
			choice := cleanText(f.value())
			if i == fields.focus {
				choice = subtleStyle.Render("◂ ") + valueStyle.Render(choice) + subtleStyle.Render(" ▸")
			}
			lines = append(lines, marker+labelStyle.Render(padRight(label, labelWidth))+" "+choice)
		default:
			// The input's "> " prompt and the cell its cursor takes past
			// the last character come out of the width it is given.
			f.line.SetWidth(max(valueWidth-3, 4))
			f.line.SetCursor(f.line.Position())
			lines = append(lines, marker+labelStyle.Render(padRight(label, labelWidth))+" "+f.line.View())
		}
	}
	for i, line := range lines {
		lines[i] = cellTruncate(line, inner, "…")
	}
	return lines, starts
}

// fieldWindow is the rows of the field block that fit in room, keeping the
// focused field's first line, and as much of it as fits, on screen.
func (m *Model) fieldWindow(lines []string, starts []int, room int) []string {
	if len(lines) <= room {
		return lines
	}
	fields := m.extView.fields
	start := starts[fields.focus]
	end := len(lines)
	if fields.focus+1 < len(starts) {
		end = starts[fields.focus+1]
	}
	top := max(0, min(start, len(lines)-room))
	if end-top > room {
		top = start
	}
	return lines[top:min(top+room, len(lines))]
}

// fieldHint is the keys every view with fields has, beside its screen's own.
func (m *Model) fieldHint() [][2]string {
	submit := "ctrl+s"
	for _, binding := range m.km().Bindings(m.extView.screen) {
		if binding.Action == ActionSubmit && len(binding.Keys) > 0 {
			submit = keymap.Display(binding.Keys[0])
		}
	}
	return [][2]string{{"tab", "next field"}, {submit, "submit"}}
}
