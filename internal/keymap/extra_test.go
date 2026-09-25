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
		{Context: ContextList, Action: "open_item", Keys: []string{"c"}, Label: "open the item"},
		{Context: ContextList, Action: "grab_quit", Keys: []string{"q", "Q"}, Label: "wants quit's key"},
	})
	if len(problems) != 1 || problems[0].Action != "grab_quit" || problems[0].Key != "q" {
		t.Fatalf("problems: %v", problems)
	}
	if action, ok := m.Action(ContextList, "c"); !ok || action != "open_item" {
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
		{Context: "detail", Action: Close, Keys: []string{"esc"}, Label: "close", Required: true},
		{Context: "detail", Action: "refresh", Keys: []string{"r"}, Label: "refresh"},
	})
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	contexts := m.Contexts()
	if contexts[len(contexts)-1] != "detail" {
		t.Fatalf("contexts: %v", contexts)
	}
	if action, _ := m.Action("detail", "r"); action != "refresh" {
		t.Fatalf("r on detail answers %q", action)
	}
	// r stays rename on the list: a screen's bindings are its own.
	if action, _ := m.Action(ContextList, "r"); action != RenameSelf {
		t.Fatalf("r on the list answers %q", action)
	}
	if _, problems := m.Rebind("detail", Close, nil); len(problems) == 0 {
		t.Fatal("a required extension action was unbound")
	}
	got := m.Bindings("detail")
	if len(got) != 2 || got[1].Action != "refresh" || got[1].Keys[0] != "r" {
		t.Fatalf("bindings: %+v", got)
	}
}

func TestExtraBindingsTheCatalogCannotAccept(t *testing.T) {
	cases := []Binding{
		{Context: ContextList, Action: Quit, Keys: []string{"z"}},
		{Context: ContextList, Action: "must_have", Keys: []string{"z"}, Required: true},
		{Context: "Detail", Action: "refresh", Keys: []string{"z"}},
		{Context: "detail", Action: "refresh.rule", Keys: []string{"z"}},
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
	m, problems := NewWith(nil, []Binding{{Context: ContextList, Action: "item", Keys: []string{"3", "c"}}})
	if len(problems) != 1 || problems[0].Key != "3" {
		t.Fatalf("problems: %v", problems)
	}
	if got := m.Keys(ContextList, "item"); len(got) != 1 || got[0] != "c" {
		t.Fatalf("keys: %v", got)
	}
}

// The operator's file reaches an extension's keys the way it reaches the
// catalog's, and a rebind keeps the extension's bindings in the map.
func TestExtraBindingsAreRebindableAndSaved(t *testing.T) {
	extra := []Binding{
		{Context: ContextList, Action: "item", Keys: []string{"c"}},
		{Context: "detail", Action: "refresh", Keys: []string{"r"}},
	}
	m, problems := NewWith(Overrides{"detail": {"refresh": {"s"}}}, extra)
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	if action, _ := m.Action("detail", "s"); action != "refresh" {
		t.Fatalf("override did not apply: s answers %q", action)
	}
	next, problems := m.Rebind(ContextList, "item", []string{"C"})
	if len(problems) != 0 {
		t.Fatalf("rebind problems: %v", problems)
	}
	if action, _ := next.Action("detail", "s"); action != "refresh" {
		t.Fatal("the rebind lost the other extension override")
	}
	text := Encode(next.Overrides())
	if !strings.Contains(text, "[detail]") || !strings.Contains(text, `item = ["C"]`) {
		t.Fatalf("saved file:\n%s", text)
	}
	back, err := Decode(text)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := NewWith(back, extra); again.Key("detail", "refresh") != "s" || again.Key(ContextList, "item") != "C" {
		t.Fatal("the saved file did not round-trip")
	}
}

// A board started without the extension still carries the operator's keys
// for it through a rebind of something else, so switching an extension off
// for a day does not cost its bindings.
func TestOverridesForAnAbsentExtensionSurviveARebind(t *testing.T) {
	m, problems := New(Overrides{
		"detail":    {"refresh": {"s"}},
		ContextList: {"item": {"C"}},
	})
	if len(problems) != 2 {
		t.Fatalf("problems: %v", problems)
	}
	next, _ := m.Rebind(ContextList, NewGroup, []string{"y"})
	got := next.Overrides()
	if keys := got["detail"]["refresh"]; len(keys) != 1 || keys[0] != "s" {
		t.Fatalf("detail.refresh: %v", got)
	}
	if keys := got[ContextList]["item"]; len(keys) != 1 || keys[0] != "C" {
		t.Fatalf("list.item: %v", got)
	}
	if keys := got[ContextList][NewGroup]; len(keys) != 1 || keys[0] != "y" {
		t.Fatalf("list.new_group: %v", got)
	}
}

// An override under an action's old name applies to the action that took it
// over, is reported as renamed rather than unknown, and is saved under the
// new name.
func TestAliasCarriesAnOldActionsKeys(t *testing.T) {
	extra := []Binding{{Context: ContextList, Action: "items_view", Keys: []string{"C"}, Label: "only items"}}
	aliases := []Alias{{Context: ContextList, From: "items_filter", To: "items_view"}}
	m, problems := NewWithAliases(Overrides{ContextList: {"items_filter": {"U"}}}, extra, aliases)
	if len(problems) != 1 || problems[0].Action != "items_filter" || !strings.Contains(problems[0].Reason, "renamed to items_view") {
		t.Fatalf("problems: %v, want the old name reported as renamed", problems)
	}
	if action, _ := m.Action(ContextList, "U"); action != "items_view" {
		t.Fatalf("U answers %q, want the old name's key on the new action", action)
	}
	if _, ok := m.Action(ContextList, "C"); ok {
		t.Fatal("the new action kept its default beside the carried key")
	}
	saved := m.Overrides()[ContextList]
	if _, kept := saved["items_filter"]; kept || !sameKeys(saved["items_view"], []string{"U"}) {
		t.Fatalf("saved = %v, want the keys under the new name alone", saved)
	}
	// A rebind resolves against the same aliases.
	next, problems := m.Rebind(ContextList, "items_view", []string{"B"})
	if len(problems) != 0 {
		t.Fatalf("rebind problems: %v", problems)
	}
	if action, _ := next.Action(ContextList, "B"); action != "items_view" {
		t.Fatalf("B answers %q after the rebind", action)
	}
}

// The new name's own override wins over the old one's.
func TestAliasLosesToTheNewNamesOwnOverride(t *testing.T) {
	extra := []Binding{{Context: ContextList, Action: "items_view", Keys: []string{"C"}, Label: "only items"}}
	aliases := []Alias{{Context: ContextList, From: "items_filter", To: "items_view"}}
	m, problems := NewWithAliases(Overrides{ContextList: {"items_filter": {"U"}, "items_view": {"B"}}}, extra, aliases)
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "whose own binding is used") {
		t.Fatalf("problems: %v", problems)
	}
	if action, _ := m.Action(ContextList, "B"); action != "items_view" {
		t.Fatalf("B answers %q", action)
	}
	if _, ok := m.Action(ContextList, "U"); ok {
		t.Fatal("the old name's key was bound as well")
	}
}

func TestAliasesTheMapRefuses(t *testing.T) {
	extra := []Binding{
		{Context: ContextList, Action: "items_view", Keys: []string{"C"}, Label: "only items"},
		{Context: ContextList, Action: "items_open", Keys: []string{"B"}, Label: "open"},
	}
	_, problems := NewWithAliases(nil, extra, []Alias{
		{Context: ContextList, From: "Bad-Name", To: "items_view"},
		{Context: ContextList, From: "quit", To: "items_view"},
		{Context: ContextList, From: "old_items", To: "items_view"},
		{Context: ContextList, From: "old_items", To: "items_open"},
		{Context: ContextList, From: "old_other", To: "not_here"},
	})
	var got []string
	for _, problem := range problems {
		got = append(got, string(problem.Action))
	}
	if strings.Join(got, ",") != "Bad-Name,quit,old_items,old_other" {
		t.Fatalf("problems: %v", problems)
	}
}
