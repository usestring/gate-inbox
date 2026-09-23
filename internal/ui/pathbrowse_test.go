package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// browseRoot is a directory holding a subdirectory that itself has children,
// and one that has none, so a walk can go both deeper and nowhere.
func browseRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"alpha", "alpha/inner", "beta"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// focusGroupPath opens the group form on a path field already holding dir,
// which is the shape the form arrives in: the field is prefilled with the
// directory the new group would inherit.
func focusGroupPath(t *testing.T, dir string) *Model {
	t.Helper()
	m := buildModel(t)
	m.openGroupForm()
	m.groupForm.path.SetValue(dir)
	for m.groupForm.focus != gfPath {
		m.groupFormFocus(1)
	}
	return m
}

func TestPathFieldFocusListsChildren(t *testing.T) {
	root := browseRoot(t)
	m := focusGroupPath(t, root)

	if !m.pathSugg.active() {
		t.Fatal("focusing a path field on a directory should list what is inside it")
	}
	want := []string{filepath.Join(root, "alpha"), filepath.Join(root, "beta")}
	for i, path := range want {
		if m.pathSugg.suggestions[i] != path {
			t.Fatalf("suggestions = %v want %v", m.pathSugg.suggestions, want)
		}
	}
	if m.pathSugg.chosen || m.pathSugg.capturing() {
		t.Fatal("an offered listing owns nothing until an arrow key enters it")
	}
}

func TestOfferedListingLeavesFormKeysAlone(t *testing.T) {
	root := browseRoot(t)

	m := focusGroupPath(t, root)
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.groupForm.focus != gfName {
		t.Fatalf("tab on an offered listing should move on, focus=%d", m.groupForm.focus)
	}
	if m.groupForm.path.Value() != root {
		t.Fatalf("tab should not have completed anything, path=%q", m.groupForm.path.Value())
	}

	m = focusGroupPath(t, root)
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.mode != modeList {
		t.Fatalf("esc on an offered listing should cancel the form, mode=%v", m.mode)
	}
}

func TestPathBrowseDescendsAndAscends(t *testing.T) {
	root := browseRoot(t)
	m := focusGroupPath(t, root)

	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !m.pathSugg.browsing {
		t.Fatal("down should enter the listing")
	}
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyRight})

	if got, want := m.groupForm.path.Value(), filepath.Join(root, "alpha")+"/"; got != want {
		t.Fatalf("right should step into the highlighted directory, path=%q want %q", got, want)
	}
	if len(m.pathSugg.suggestions) != 1 || m.pathSugg.suggestions[0] != filepath.Join(root, "alpha", "inner") {
		t.Fatalf("listing should be what is inside alpha, got %v", m.pathSugg.suggestions)
	}
	if m.groupForm.pathAuto {
		t.Fatal("a walked-to path is a choice, not a default the form may overwrite")
	}

	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got, want := m.groupForm.path.Value(), root+"/"; got != want {
		t.Fatalf("left should step back out, path=%q want %q", got, want)
	}
	if m.pathSugg.selected() != filepath.Join(root, "alpha") {
		t.Fatalf("stepping out should land back on the directory just left, got %q", m.pathSugg.selected())
	}
}

func TestPathBrowseIntoEmptyDirectoryKeepsWalk(t *testing.T) {
	root := browseRoot(t)
	m := focusGroupPath(t, root)

	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.pathSugg.selected() != filepath.Join(root, "beta") {
		t.Fatalf("expected beta highlighted, got %q", m.pathSugg.selected())
	}
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyRight})

	if got, want := m.groupForm.path.Value(), filepath.Join(root, "beta")+"/"; got != want {
		t.Fatalf("a childless directory is still a pick, path=%q want %q", got, want)
	}
	if m.pathSugg.active() {
		t.Fatalf("nothing to list under beta, got %v", m.pathSugg.suggestions)
	}
	if !m.pathSugg.showing() || !m.pathSugg.browsing {
		t.Fatal("the walk should stay open so left can step back out")
	}

	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.pathSugg.selected() != filepath.Join(root, "beta") {
		t.Fatalf("left out of an empty directory should land back on it, got %q", m.pathSugg.selected())
	}
}

func TestPathBrowseSubmitsWalkedDirectory(t *testing.T) {
	root := browseRoot(t)
	m := focusGroupPath(t, root)
	m.groupForm.name.SetValue("walked")

	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyRight})
	// The first enter takes the highlighted directory, the second creates.
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	groups, err := m.store.Groups()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Name == "walked" {
			if want := filepath.Join(root, "alpha", "inner"); g.Path != want {
				t.Fatalf("group path = %q want %q", g.Path, want)
			}
			return
		}
	}
	t.Fatalf("group was not created, err=%q", m.errBar.text)
}

func TestTypedPathStillCompletesOnTab(t *testing.T) {
	root := browseRoot(t)
	m := focusGroupPath(t, root)

	typed := filepath.Join(root, "al")
	m.groupForm.path.SetValue(typed)
	m.pathSugg.recompute(typed)
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyTab})

	if got, want := m.groupForm.path.Value(), filepath.Join(root, "alpha")+"/"; got != want {
		t.Fatalf("tab on a typed prefix should complete it, path=%q want %q", got, want)
	}
	if m.groupForm.focus != gfPath {
		t.Fatalf("completing should not leave the field, focus=%d", m.groupForm.focus)
	}
}
