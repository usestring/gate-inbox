package ui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension"
)

// itemsObserver is itemsExtension that also hears of spawns from the form,
// noting whether the board had already focused the pane when it was told.
type itemsObserver struct {
	itemsExtension
	m        *Model
	openView bool
	heard    []heardSpawn
}

type heardSpawn struct {
	id      string
	form    map[string]string
	focused bool
}

func (c *itemsObserver) FormSpawned(_ context.Context, sess extension.SessionInfo, form map[string]string) {
	c.heard = append(c.heard, heardSpawn{id: sess.ID, form: form, focused: c.m.mode == modeFocus})
	if c.openView {
		c.m.noteExtensionViewOpened()
	}
}

func submitItemsForm(t *testing.T, m *Model) {
	t.Helper()
	m.openForm()
	m.form.name.SetValue("with-items")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = toolIndexOf(t, m, "claude-hooked")
	pickGroup(t, m, "")
	_, cmd := m.submitForm()
	m.applyCmd(t, cmd)
}

// An extension hears of a spawn from the form, with its own values, before
// the board focuses the new pane, and the board focuses it afterwards.
func TestFormSpawnIsHeardBeforeThePaneIsFocused(t *testing.T) {
	m := buildModel(t)
	obs := &itemsObserver{m: m}
	useItemsExtension(t, obs)
	submitItemsForm(t, m)

	if len(obs.heard) != 1 {
		t.Fatalf("the extension heard of %d spawns from the form, want 1", len(obs.heard))
	}
	got := obs.heard[0]
	if got.focused {
		t.Fatal("the board focused the pane before the extension heard of the spawn")
	}
	if got.form["items"] != extension.FormOff || got.form["level"] != "quick" || len(got.form) != 2 {
		t.Fatalf("the extension was handed %v, want its own two fields at their defaults", got.form)
	}
	if m.mode != modeFocus || m.focusedID != got.id {
		t.Fatalf("mode %v on %q, want the new session %q focused", m.mode, m.focusedID, got.id)
	}
}

// A view the extension opens while it is told takes precedence: the cursor
// goes to the new row, but the board does not focus its pane.
func TestAViewOpenedOnAFormSpawnKeepsThePaneUnfocused(t *testing.T) {
	m := buildModel(t)
	obs := &itemsObserver{m: m, openView: true}
	useItemsExtension(t, obs)
	submitItemsForm(t, m)

	if len(obs.heard) != 1 {
		t.Fatalf("the extension heard of %d spawns from the form, want 1", len(obs.heard))
	}
	if m.mode == modeFocus {
		t.Fatal("the board focused the pane over the extension's view")
	}
	if sess, ok := m.selected(); !ok || sess.ID != obs.heard[0].id {
		t.Fatalf("the cursor is not on the new session %q", obs.heard[0].id)
	}
}

// A spawn that did not come from the form is not a form spawn.
func TestSpawnOutsideTheFormIsNotAFormSpawn(t *testing.T) {
	m := buildModel(t)
	obs := &itemsObserver{m: m}
	useItemsExtension(t, obs)
	if err := m.spawnSession("claude-hooked", "quick", t.TempDir(), "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(obs.launches) != 1 {
		t.Fatalf("the extension heard of %d launches, want 1", len(obs.launches))
	}
	if len(obs.heard) != 0 {
		t.Fatalf("a spawn outside the form was reported as one from it: %+v", obs.heard)
	}
}

// viewingObserver opens a view of its own through the extension bridge --
// the path UIHost.Open takes -- when it hears of a spawn from the form.
type viewingObserver struct {
	itemsExtension
	bridge *ExtensionBridge
	view   *stubView
	heard  []string
}

func (o *viewingObserver) FormSpawned(_ context.Context, sess extension.SessionInfo, _ map[string]string) {
	o.heard = append(o.heard, sess.ID)
	o.bridge.Open("ext1", "detail", o.view)
}

// A FormSpawned that opens a view through UIHost.Open keeps the cursor on
// the new row, opens the view once its message arrives, and never focuses
// the new pane: the bridge counts the open as it is made, before the view
// reaches the board.
func TestAFormSpawnThatOpensAViewThroughTheHostLeavesThePaneUnfocused(t *testing.T) {
	m := buildModel(t)
	bridge := NewExtensionBridge([]string{"ext1"})
	m.InstallExtensions([]ExtensionUI{{Owner: "ext1", Keys: []ExtensionKey{
		{Screen: "detail", Action: "done", Keys: []string{"x"}, Label: "mark it done"},
	}}}, bridge)
	sent := make(chan tea.Msg, 16)
	bridge.Attach(func(msg tea.Msg) { sent <- msg })
	obs := &viewingObserver{bridge: bridge, view: &stubView{}}
	useItemsExtension(t, obs)
	submitItemsForm(t, m)

	if len(obs.heard) != 1 {
		t.Fatalf("the extension heard of %d spawns from the form, want 1", len(obs.heard))
	}
	spawned := obs.heard[0]
	if m.mode == modeFocus || m.focusedID == spawned {
		t.Fatalf("the board focused the new pane (mode %v, focused %q) over the view the extension opened", m.mode, m.focusedID)
	}
	if sess, ok := m.selected(); !ok || sess.ID != spawned {
		t.Fatalf("the cursor is not on the new session %q", spawned)
	}
	for deadline := time.Now().Add(5 * time.Second); m.mode != modeExtensionView; {
		if time.Now().After(deadline) {
			t.Fatalf("the extension's view never opened: mode %v", m.mode)
		}
		deliver(t, m, sent)
	}
	if m.focusedID == spawned {
		t.Fatal("the new pane was focused once the view opened")
	}
	if sess, ok := m.selected(); !ok || sess.ID != spawned {
		t.Fatalf("the cursor left the new session %q when the view opened", spawned)
	}
}
