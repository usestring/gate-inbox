package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
)

// itemsExtension adds a toggle and a choice to the new-session form, and
// records every launch it is asked about.
type itemsExtension struct {
	launches []extension.Launch
}

func (c *itemsExtension) Descriptor() extension.Descriptor { return extension.Descriptor{ID: "ext1"} }
func (c *itemsExtension) Configure(extension.Config) error { return nil }
func (c *itemsExtension) SessionFormFields() []extension.FormField {
	return []extension.FormField{
		{Key: "items", Kind: extension.FormToggle},
		{Key: "level", Label: "level", Kind: extension.FormChoice, Options: []string{"none", "quick", "full"}, Default: "quick"},
	}
}
func (c *itemsExtension) LaunchEnv(_ context.Context, launch extension.Launch) (map[string]string, error) {
	c.launches = append(c.launches, launch)
	return nil, nil
}

func useItemsExtension(t *testing.T, c extension.Extension) {
	t.Helper()
	registry, err := extension.NewRegistry([]extension.Extension{c})
	if err != nil {
		t.Fatal(err)
	}
	if report := registry.Configure("", nil); !report.OK() {
		t.Fatal(report.Notes())
	}
	collected, err := registry.SessionHooks("", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sessionhooks.Use(func() (*extension.SessionHooks, error) { return collected, nil }))
}

// An extension's fields follow the form's own: up from the first field
// wraps onto the last of them, space flips a toggle, the arrows cycle a
// choice, and what they were set to reaches the extension at launch.
func TestFormFieldsReachTheExtensionAtLaunch(t *testing.T) {
	m := buildModel(t)
	items := &itemsExtension{}
	useItemsExtension(t, items)
	m.openForm()
	if len(m.form.extra) != 2 {
		t.Fatalf("the form has %d extension fields, want 2", len(m.form.extra))
	}
	m.form.name.SetValue("with-items")
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = toolIndexOf(t, m, "claude-hooked")
	pickGroup(t, m, "")

	m.handleFormKey(key("up"))
	if e, ok := m.form.focusedExtra(); !ok || e.field.Key != "ext1/level" {
		t.Fatalf("up from the first field focused %d, want the last extension field", m.form.focus)
	}
	view := m.viewForm()
	if !strings.Contains(view, "level") || !strings.Contains(view, "quick") || !strings.Contains(view, "change") {
		t.Fatalf("the form does not show the choice at its default with its hint:\n%s", view)
	}
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.handleFormKey(key("up"))
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	view = m.viewForm()
	if !strings.Contains(view, "items") || !strings.Contains(view, "toggle") {
		t.Fatalf("the form does not show the toggle with its hint:\n%s", view)
	}

	_, cmd := m.submitForm()
	m.applyCmd(t, cmd)
	if len(items.launches) != 1 {
		t.Fatalf("the extension heard of %d launches, want 1", len(items.launches))
	}
	got := items.launches[0].Form
	if got["items"] != extension.FormOn || got["level"] != "none" || len(got) != 2 {
		t.Fatalf("the launch carried %v, want items=on level=none", got)
	}
}

// A spawn that did not come from the form carries no form values.
func TestSpawnOutsideTheFormCarriesNoFormValues(t *testing.T) {
	m := buildModel(t)
	items := &itemsExtension{}
	useItemsExtension(t, items)
	if err := m.spawnSession("claude-hooked", "quick", t.TempDir(), "", "", false); err != nil {
		t.Fatal(err)
	}
	if len(items.launches) != 1 || items.launches[0].Form != nil {
		t.Fatalf("launches = %+v, want one with no form values", items.launches)
	}
}
