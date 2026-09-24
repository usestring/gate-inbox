package keymap

import (
	"strings"
	"testing"
)

// An extension's key joins the list beside the catalog's. Where it asks for a
// key the catalog already holds, the catalog keeps it and the extension is
// told, so no default the board documents can be taken by a build's add-on.
func TestExtraBindingJoinsAScreenAndLosesACollisionToTheCatalog(t *testing.T) {
	m, problems := NewWith(nil, []Binding{
		{Context: ContextList, Action: "open_card", Keys: []string{"c"}, Label: "open the card"},
		{Context: ContextList, Action: "grab_quit", Keys: []string{"q", "Q"}, Label: "wants quit's key"},
	})
	if len(problems) != 1 || problems[0].Action != "grab_quit" || problems[0].Key != "q" {
		t.Fatalf("problems: %v", problems)
	}
	if action, ok := m.Action(ContextList, "c"); !ok || action != "open_card" {
		t.Fatalf("c answers %q", action)
	}
	if action, _ := m.Action(ContextList, "q"); action != Quit {
		t.Fatalf("q answers %q, want quit to keep it", action)
	}
	if action, _ := m.Action(ContextList, "Q"); action != "grab_quit" {
		t.Fatalf("Q answers %q", action)
	}
}

// An extension may add a screen of its own, and there a binding may be
// required: the extension's own way off its screen.
func TestExtraBindingOpensAScreenOfItsOwn(t *testing.T) {
	m, problems := NewWith(nil, []Binding{
		{Context: "task", Action: Close, Keys: []string{"esc"}, Label: "close", Required: true},
		{Context: "task", Action: "nudge", Keys: []string{"r"}, Label: "nudge"},
	})
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	contexts := m.Contexts()
	if contexts[len(contexts)-1] != "task" {
		t.Fatalf("contexts: %v", contexts)
	}
	if action, _ := m.Action("task", "r"); action != "nudge" {
		t.Fatalf("r on task answers %q", action)
	}
	// r stays rename on the list: a screen's bindings are its own.
	if action, _ := m.Action(ContextList, "r"); action != RenameSelf {
		t.Fatalf("r on the list answers %q", action)
	}
	if _, problems := m.Rebind("task", Close, nil); len(problems) == 0 {
		t.Fatal("a required extension action was unbound")
	}
	got := m.Bindings("task")
	if len(got) != 2 || got[1].Action != "nudge" || got[1].Keys[0] != "r" {
		t.Fatalf("bindings: %+v", got)
	}
}

func TestExtraBindingsTheCatalogCannotAccept(t *testing.T) {
	cases := []Binding{
		{Context: ContextList, Action: Quit, Keys: []string{"z"}},
		{Context: ContextList, Action: "must_have", Keys: []string{"z"}, Required: true},
		{Context: "Task", Action: "nudge", Keys: []string{"z"}},
		{Context: "task", Action: "nudge.rule", Keys: []string{"z"}},
	}
	for _, binding := range cases {
		m, problems := NewWith(nil, []Binding{binding})
		if len(problems) != 1 || problems[0].Key != "" {
			t.Errorf("%s.%s: problems %v", binding.Context, binding.Action, problems)
		}
		if action, _ := m.Action(ContextList, "z"); action != "" {
			t.Errorf("%s.%s was added anyway", binding.Context, binding.Action)
		}
	}
	// A reserved default key is dropped; the binding keeps the rest.
	m, problems := NewWith(nil, []Binding{{Context: ContextList, Action: "card", Keys: []string{"3", "c"}}})
	if len(problems) != 1 || problems[0].Key != "3" {
		t.Fatalf("problems: %v", problems)
	}
	if got := m.Keys(ContextList, "card"); len(got) != 1 || got[0] != "c" {
		t.Fatalf("keys: %v", got)
	}
}

// The operator's file reaches an extension's keys the way it reaches the
// catalog's, and a rebind keeps the extension's bindings in the map.
func TestExtraBindingsAreRebindableAndSaved(t *testing.T) {
	extra := []Binding{
		{Context: ContextList, Action: "card", Keys: []string{"c"}},
		{Context: "task", Action: "nudge", Keys: []string{"r"}},
	}
	m, problems := NewWith(Overrides{"task": {"nudge": {"s"}}}, extra)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if action, _ := m.Action("task", "s"); action != "nudge" {
		t.Fatalf("override did not apply: s answers %q", action)
	}
	next, problems := m.Rebind(ContextList, "card", []string{"C"})
	if len(problems) != 0 {
		t.Fatalf("rebind problems: %v", problems)
	}
	if action, _ := next.Action("task", "s"); action != "nudge" {
		t.Fatal("the rebind lost the other extension override")
	}
	text := Encode(next.Overrides())
	if !strings.Contains(text, "[task]") || !strings.Contains(text, `card = ["C"]`) {
		t.Fatalf("saved file:\n%s", text)
	}
	back, err := Decode(text)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := NewWith(back, extra); again.Key("task", "nudge") != "s" || again.Key(ContextList, "card") != "C" {
		t.Fatal("the saved file did not round-trip")
	}
}

// A board started without the extension still carries the operator's keys
// for it through a rebind of something else, so switching an extension off
// for a day does not cost its bindings.
func TestOverridesForAnAbsentExtensionSurviveARebind(t *testing.T) {
	m, problems := New(Overrides{
		"task":       {"nudge": {"s"}},
		ContextList: {"card": {"C"}},
	})
	if len(problems) != 2 {
		t.Fatalf("problems: %v", problems)
	}
	next, _ := m.Rebind(ContextList, NewGroup, []string{"y"})
	got := next.Overrides()
	if keys := got["task"]["nudge"]; len(keys) != 1 || keys[0] != "s" {
		t.Fatalf("task.nudge: %v", got)
	}
	if keys := got[ContextList]["card"]; len(keys) != 1 || keys[0] != "C" {
		t.Fatalf("list.card: %v", got)
	}
	if keys := got[ContextList][NewGroup]; len(keys) != 1 || keys[0] != "y" {
		t.Fatalf("list.new_group: %v", got)
	}
}
