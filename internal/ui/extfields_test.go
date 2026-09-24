package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// formView is a stubView with fields.
type formView struct {
	stubView
	fields []ViewField
}

func (v *formView) Fields() []ViewField { return v.fields }

var (
	tabKey      = tea.KeyPressMsg{Code: tea.KeyTab}
	shiftTabKey = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	ctrlSKey    = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	leftKey     = tea.KeyPressMsg{Code: tea.KeyLeft}
	rightKey    = tea.KeyPressMsg{Code: tea.KeyRight}
)

func sampleForm() *formView {
	return &formView{
		stubView: stubView{lines: [][]Span{{{Text: "a sample form"}}}},
		fields: []ViewField{
			{ID: "title", Label: "title", Value: "topic"},
			{ID: "notes", Label: "notes", Multiline: true, Placeholder: "anything else"},
			{ID: "color", Label: "color", Choices: []string{"red", "blue"}, Value: "blue"},
		},
	}
}

// The board owns the typing: keys land in the focused field and never reach
// the view, tab and shift+tab move between fields, a multi-line box takes
// enter as a line break and a paste whole, a choice moves on left and right,
// and submit hands every value back with the field that had the keyboard.
func TestAFormsFieldsAreTypedIntoByTheBoard(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := sampleForm()
	bridge.Open("items", "detail", view)
	deliver(t, m, sent)
	if m.extView.fields == nil {
		t.Fatal("the form's fields were not read when it opened")
	}

	// r is the screen's refresh key, but a field with the keyboard takes it
	// as a letter.
	typeInto(t, m, " rest")
	m.handleKey(tabKey)
	typeInto(t, m, "line one")
	m.handleKey(key("enter"))
	m.Update(tea.PasteMsg{Content: "line two\nline three"})
	m.handleKey(tabKey)
	m.handleKey(leftKey)
	if len(view.keys) != 0 {
		t.Fatalf("the view was told typing: %+v", view.keys)
	}

	m.handleKey(ctrlSKey)
	if len(view.keys) != 1 {
		t.Fatalf("keys = %+v, want the one submit", view.keys)
	}
	got := view.keys[0]
	want := map[string]string{"title": "topic rest", "notes": "line one\nline two\nline three", "color": "red"}
	if got.Action != string(ActionSubmit) || got.Field != "color" || len(got.Values) != len(want) {
		t.Fatalf("submit = %+v", got)
	}
	for id, value := range want {
		if got.Values[id] != value {
			t.Errorf("%s = %q, want %q", id, got.Values[id], value)
		}
	}

	// Enter submits from a one-line field; shift+tab walks back to it.
	m.handleKey(shiftTabKey)
	m.handleKey(shiftTabKey)
	m.handleKey(key("enter"))
	if len(view.keys) != 2 || view.keys[1].Action != string(ActionSubmit) || view.keys[1].Field != "title" {
		t.Fatalf("keys = %+v, want enter on the title to submit", view.keys)
	}
	// A choice types nothing, so the screen's own keys reach the view there.
	m.handleKey(shiftTabKey)
	m.handleKey(rightKey)
	m.handleKey(key("r"))
	if last := view.keys[len(view.keys)-1]; last.Action != "refresh" || last.Values["color"] != "blue" {
		t.Fatalf("keys = %+v, want refresh told with the choice moved back", view.keys)
	}
}

// Fields are matched by ID from one draw to the next: typing survives a
// redraw, a new Value from the view replaces it, a dropped field goes, and
// the keyboard stays on the field it was on.
func TestAFormsFieldsFollowTheView(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := sampleForm()
	bridge.Open("items", "detail", view)
	deliver(t, m, sent)
	m.handleKey(tabKey)
	typeInto(t, m, "draft")
	m.viewExtension()
	if got := m.extView.fields.values()["notes"]; got != "draft" {
		t.Fatalf("notes = %q after a redraw, want the typing kept", got)
	}

	view.fields = []ViewField{
		{ID: "notes", Label: "notes", Multiline: true, Value: "loaded\x1b[2J notes"},
		{ID: "title", Label: "title", Value: "topic"},
	}
	m.viewExtension()
	values := m.extView.fields.values()
	if values["notes"] != "loaded[2J notes" || values["title"] != "topic" || len(values) != 2 {
		t.Fatalf("values = %v, want the offered notes (cleaned) and no color", values)
	}
	if m.extView.fields.order[m.extView.fields.focus] != "notes" {
		t.Fatal("the keyboard left the field it was on when the fields were reordered")
	}

	// A field that changes kind starts over as the new kind.
	view.fields = []ViewField{{ID: "notes", Label: "notes", Choices: []string{"one", "two"}, Value: "two"}}
	m.viewExtension()
	if got := m.extView.fields.values()["notes"]; got != "two" {
		t.Fatalf("notes = %q after it became a choice", got)
	}

	view.fields = nil
	m.viewExtension()
	if m.extView.fields != nil {
		t.Fatal("a form with no fields left kept its inputs")
	}
	m.handleKey(key("r"))
	if last := view.keys[len(view.keys)-1]; last.Action != "refresh" || last.Values != nil {
		t.Fatalf("keys = %+v, want a plain view's press", view.keys)
	}
}

// The card draws the view's lines, then each field under its label, and the
// hint says how to move and submit; a form taller than the card keeps the
// focused field in view.
func TestAFormIsDrawnUnderTheViewsLines(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := sampleForm()
	bridge.Open("items", "detail", view)
	deliver(t, m, sent)
	frame := ansi.Strip(m.viewExtension())
	for _, want := range []string{"a sample form", "title", "topic", "notes", "anything else", "color", "blue", "tab/↑↓", "move", "ctrl+s", "submit", "refresh it"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	if strings.Index(frame, "a sample form") > strings.Index(frame, "color") {
		t.Fatalf("the fields were drawn above the view's lines:\n%s", frame)
	}

	for i := range 12 {
		view.fields = append(view.fields, ViewField{ID: "box" + string(rune('a'+i)), Label: "box " + string(rune('a'+i)), Multiline: true, Height: 4})
	}
	view.fields = append(view.fields, ViewField{ID: "last", Label: "the last one", Value: "at the bottom"})
	m.height = 30
	m.viewExtension()
	for range len(view.fields) - 1 {
		m.handleKey(tabKey)
	}
	frame = ansi.Strip(m.viewExtension())
	if !strings.Contains(frame, "the last one") || !strings.Contains(frame, "at the bottom") {
		t.Fatalf("the focused field at the end of a long form is off the card:\n%s", frame)
	}
	if !strings.Contains(frame, "a sample form") {
		t.Fatalf("a long form pushed the view's own lines off the card:\n%s", frame)
	}
}

// A screen that binds submit submits on its key, and ctrl+s is then just a
// key the field takes.
func TestAScreensSubmitKeyReplacesCtrlS(t *testing.T) {
	m, bridge, sent := viewModel(t,
		ExtensionKey{Screen: "detail", Action: string(ActionSubmit), Keys: []string{"ctrl+d"}, Label: "save it"},
	)
	view := sampleForm()
	bridge.Open("items", "detail", view)
	deliver(t, m, sent)
	m.handleKey(tabKey)
	m.handleKey(ctrlSKey)
	if len(view.keys) != 0 {
		t.Fatalf("ctrl+s submitted on a screen with its own submit key: %+v", view.keys)
	}
	m.handleKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if len(view.keys) != 1 || view.keys[0].Action != string(ActionSubmit) || view.keys[0].Key != "ctrl+d" {
		t.Fatalf("keys = %+v, want the screen's submit", view.keys)
	}
	if frame := ansi.Strip(m.viewExtension()); !strings.Contains(frame, "ctrl+d") {
		t.Fatalf("the hint does not show the screen's submit key:\n%s", frame)
	}
}

// Up and down move between fields as tab and shift+tab do, wrapping at the
// ends. A multi-line box keeps them for its own rows, and hands them on only
// from its first row going up or its last row going down.
func TestUpAndDownMoveBetweenAFormsFields(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := sampleForm()
	bridge.Open("items", "detail", view)
	deliver(t, m, sent)
	focusedID := func() string {
		fields := m.extView.fields
		return fields.order[fields.focus]
	}
	step := func(k, want string) {
		t.Helper()
		m.handleKey(key(k))
		if got := focusedID(); got != want {
			t.Fatalf("after %s the focus is on %q, want %q", k, got, want)
		}
	}

	step("down", "notes")
	typeInto(t, m, "one")
	m.handleKey(key("enter"))
	typeInto(t, m, "two")
	// The cursor is on the box's last row: up takes it to the first row,
	// and only the next up leaves the box.
	step("up", "notes")
	step("up", "title")
	// Back in, the cursor is where it was left, on the first row.
	step("down", "notes")
	step("down", "notes")
	step("down", "color")
	step("down", "title")
	step("up", "color")
	step("up", "notes")
	step("up", "notes")
	step("up", "title")

	if len(view.keys) != 0 {
		t.Fatalf("the view was told a move: %+v", view.keys)
	}
	m.handleKey(ctrlSKey)
	got := view.keys[0].Values
	if got["notes"] != "one\ntwo" || got["color"] != "blue" || got["title"] != "topic" {
		t.Fatalf("values = %+v, want the moves to have changed nothing", got)
	}
}
