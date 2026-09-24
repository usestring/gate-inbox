package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// formView is a runView with fields.
type formView struct {
	runView
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

func formFixture() *formView {
	return &formView{
		runView: runView{lines: [][]Span{{{Text: "a card for the job"}}}},
		fields: []ViewField{
			{ID: "title", Label: "title", Value: "topic"},
			{ID: "goal", Label: "goal", Multiline: true, Placeholder: "done when"},
			{ID: "backend", Label: "backend", Choices: []string{"full", "fast"}, Value: "fast"},
		},
	}
}

// The board owns the typing: keys land in the focused field and never reach
// the view, tab and shift+tab move between fields, a multi-line box takes
// enter as a line break and a paste whole, a choice moves on left and right,
// and submit hands every value back with the field that had the keyboard.
func TestAFormsFieldsAreTypedIntoByTheBoard(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := formFixture()
	bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	if m.extView.fields == nil {
		t.Fatal("the form's fields were not read when it opened")
	}

	// r is the screen's nudge key, but a field with the keyboard takes it
	// as a letter.
	typeInto(t, m, " run")
	m.handleKey(tabKey)
	typeInto(t, m, "every")
	m.handleKey(key("enter"))
	m.Update(tea.PasteMsg{Content: "region\nby friday"})
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
	want := map[string]string{"title": "topic run", "goal": "every\nregion\nby friday", "backend": "full"}
	if got.Action != string(ActionSubmit) || got.Field != "backend" || len(got.Values) != len(want) {
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
	if last := view.keys[len(view.keys)-1]; last.Action != "nudge" || last.Values["backend"] != "fast" {
		t.Fatalf("keys = %+v, want nudge told with the choice moved back", view.keys)
	}
}

// Fields are matched by ID from one draw to the next: typing survives a
// redraw, a new Value from the view replaces it, a dropped field goes, and
// the keyboard stays on the field it was on.
func TestAFormsFieldsFollowTheView(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := formFixture()
	bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	m.handleKey(tabKey)
	typeInto(t, m, "draft")
	m.viewExtension()
	if got := m.extView.fields.values()["goal"]; got != "draft" {
		t.Fatalf("goal = %q after a redraw, want the typing kept", got)
	}

	view.fields = []ViewField{
		{ID: "goal", Label: "goal", Multiline: true, Value: "loaded\x1b[2J goal"},
		{ID: "title", Label: "title", Value: "topic"},
	}
	m.viewExtension()
	values := m.extView.fields.values()
	if values["goal"] != "loaded[2J goal" || values["title"] != "topic" || len(values) != 2 {
		t.Fatalf("values = %v, want the offered goal (cleaned) and no backend", values)
	}
	if m.extView.fields.order[m.extView.fields.focus] != "goal" {
		t.Fatal("the keyboard left the field it was on when the fields were reordered")
	}

	// A field that changes kind starts over as the new kind.
	view.fields = []ViewField{{ID: "goal", Label: "goal", Choices: []string{"one", "two"}, Value: "two"}}
	m.viewExtension()
	if got := m.extView.fields.values()["goal"]; got != "two" {
		t.Fatalf("goal = %q after it became a choice", got)
	}

	view.fields = nil
	m.viewExtension()
	if m.extView.fields != nil {
		t.Fatal("a form with no fields left kept its inputs")
	}
	m.handleKey(key("r"))
	if last := view.keys[len(view.keys)-1]; last.Action != "nudge" || last.Values != nil {
		t.Fatalf("keys = %+v, want a plain view's press", view.keys)
	}
}

// The card draws the view's lines, then each field under its label, and the
// hint says how to move and submit; a form taller than the card keeps the
// focused field in view.
func TestAFormIsDrawnUnderTheViewsLines(t *testing.T) {
	m, bridge, sent := viewModel(t)
	view := formFixture()
	bridge.Open("cards", "task", view)
	deliver(t, m, sent)
	frame := ansi.Strip(m.viewExtension())
	for _, want := range []string{"a card for the job", "title", "topic", "goal", "done when", "backend", "fast", "next field", "ctrl+s", "submit", "nudge it"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
	if strings.Index(frame, "a card for the job") > strings.Index(frame, "backend") {
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
	if !strings.Contains(frame, "a card for the job") {
		t.Fatalf("a long form pushed the view's own lines off the card:\n%s", frame)
	}
}

// A screen that binds submit submits on its key, and ctrl+s is then just a
// key the field takes.
func TestAScreensSubmitKeyReplacesCtrlS(t *testing.T) {
	m, bridge, sent := viewModel(t,
		ExtensionKey{Screen: "task", Action: string(ActionSubmit), Keys: []string{"ctrl+d"}, Label: "save it"},
	)
	view := formFixture()
	bridge.Open("cards", "task", view)
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
