// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func setupCompletionDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"alpha", "amber", "beta", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCompleteDirsMatchesPrefix(t *testing.T) {
	root := setupCompletionDir(t)
	got := completeDirs(filepath.Join(root, "a"))
	want := []string{filepath.Join(root, "alpha"), filepath.Join(root, "amber")}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestCompleteDirsTrailingSlashListsChildren(t *testing.T) {
	root := setupCompletionDir(t)
	got := completeDirs(root + "/")
	if len(got) != 3 {
		t.Fatalf("expected 3 visible dirs, got %v", got)
	}
	for _, path := range got {
		if filepath.Base(path) == ".hidden" || filepath.Base(path) == "afile" {
			t.Fatalf("unexpected entry %s", path)
		}
	}
}

func TestCompleteDirsHiddenNeedsDotPrefix(t *testing.T) {
	root := setupCompletionDir(t)
	got := completeDirs(filepath.Join(root, ".h"))
	if len(got) != 1 || filepath.Base(got[0]) != ".hidden" {
		t.Fatalf("got %v", got)
	}
}

func TestCompleteDirsNoSlashNoSuggestions(t *testing.T) {
	if got := completeDirs("relative"); got != nil {
		t.Fatalf("got %v", got)
	}
	if got := completeDirs(""); got != nil {
		t.Fatalf("got %v", got)
	}
}

func TestApplyPathSuggestionFillsDirField(t *testing.T) {
	root := setupCompletionDir(t)
	m := &Model{mode: modeForm}
	m.form.dir = textField("", 400)
	m.pathSugg.recompute(filepath.Join(root, "al"))
	if !m.pathSugg.active() {
		t.Fatal("expected suggestions")
	}
	m.applyPathSuggestion()
	want := filepath.Join(root, "alpha") + "/"
	if m.form.dir.Value() != want {
		t.Fatalf("dir = %q want %q", m.form.dir.Value(), want)
	}
	if m.form.dirAuto {
		t.Fatal("dirAuto should be cleared after completion")
	}
}

func TestPathSuggestionsExitToAdjacentFormFields(t *testing.T) {
	root := setupCompletionDir(t)
	m := buildModel(t)
	m.openForm()
	m.form.focus = fieldDir
	m.form.name.Blur()
	m.form.dir.Focus()
	m.pathSugg.recompute(filepath.Join(root, "a"))

	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if !m.pathSugg.chosen || m.pathSugg.index != 0 {
		t.Fatalf("first down should select the first suggestion, chosen=%v index=%d",
			m.pathSugg.chosen, m.pathSugg.index)
	}
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.pathSugg.index != 1 {
		t.Fatalf("second down should select the second suggestion, index=%d", m.pathSugg.index)
	}
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.form.focus != fieldTool {
		t.Fatalf("down past the last suggestion should leave the last field for the first, focus=%d", m.form.focus)
	}

	m.formFocus(-1)
	m.pathSugg.recompute(filepath.Join(root, "a"))
	m.pathSugg.chosen = true
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.form.focus != fieldGroup {
		t.Fatalf("up past the first suggestion should focus the group above it, focus=%d", m.form.focus)
	}
}

func TestGroupPickerExitsToAdjacentFields(t *testing.T) {
	m := buildModel(t)

	m.openGroupForm()
	m.groupFormFocus(1)
	m.form.groupIndex = 0
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.groupForm.focus != gfName {
		t.Fatalf("up past first parent should focus name, focus=%d", m.groupForm.focus)
	}

	m.openGroupForm()
	m.groupFormFocus(1)
	m.form.groupIndex = len(m.form.groups) - 1
	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.groupForm.focus != gfPath {
		t.Fatalf("down past last parent should focus path, focus=%d", m.groupForm.focus)
	}

	m.openForm()
	m.form.focus = fieldGroup
	m.form.name.Blur()
	m.form.groupIndex = len(m.form.groups) - 1
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.form.focus != fieldDir {
		t.Fatalf("down past last group should focus dir, focus=%d", m.form.focus)
	}
}

func TestStandaloneGroupPickerWrapsWhenThereAreNoAdjacentFields(t *testing.T) {
	m := buildModel(t)
	m.form.groups = []groupOption{{path: ""}, {path: "alpha"}, {path: "beta"}}
	m.mode = modeMove

	m.form.groupIndex = 0
	m.handleMoveKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.form.groupIndex != 2 {
		t.Fatalf("up past first group should wrap to last, index=%d", m.form.groupIndex)
	}

	m.handleMoveKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.form.groupIndex != 0 {
		t.Fatalf("down past last group should wrap to first, index=%d", m.form.groupIndex)
	}
}

func TestGroupEditPathSuggestionsExitToWorktree(t *testing.T) {
	root := setupCompletionDir(t)
	m := buildModel(t)
	if err := m.store.CreateGroup("backend", t.TempDir()); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "backend")
	m.openRename()
	m.groupForm.focus = gfPath
	m.groupForm.path.Focus()
	m.pathSugg.recompute(filepath.Join(root, "a"))
	m.pathSugg.chosen = true
	m.pathSugg.index = len(m.pathSugg.suggestions) - 1

	m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.groupForm.focus != gfName {
		t.Fatalf("down past last suggestion should wrap to name, focus=%d", m.groupForm.focus)
	}
}

func TestGroupFormInheritsParentPath(t *testing.T) {
	m := buildModel(t)
	parentPath := t.TempDir()
	if err := m.store.CreateGroup("projects", parentPath); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())

	// The cursor starts on root, which has no stored path to inherit.
	m.selectGroupRow(t, "projects")
	m.openGroupForm()
	if m.groupForm.path.Value() != parentPath {
		t.Fatalf("cursor on group should inherit its path, got %q want %q", m.groupForm.path.Value(), parentPath)
	}
	pickGroup(t, m, "")
	m.moveGroupCursor(0)
	if m.groupForm.path.Value() != "" {
		t.Fatalf("root parent should clear auto path, got %q", m.groupForm.path.Value())
	}

	m.groupForm.path.SetValue("/custom")
	m.groupForm.pathAuto = false
	pickGroup(t, m, "")
	m.moveGroupCursor(0)
	if m.groupForm.path.Value() != "/custom" {
		t.Fatalf("manual path should survive parent change, got %q", m.groupForm.path.Value())
	}
}

func TestAncestorGroupPathWalksUp(t *testing.T) {
	root := t.TempDir()
	m := &Model{groupPaths: map[string]string{"projects": root}}
	if got := m.ancestorGroupDir("projects/api/auth"); got != root {
		t.Fatalf("got %q want %q", got, root)
	}
	if got := m.ancestorGroupDir("other"); got != "" {
		t.Fatalf("got %q want empty", got)
	}
}

func TestRelativePathsStoredAbsolute(t *testing.T) {
	m := buildModel(t)
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(base)

	m.openGroupForm()
	m.groupForm.name.SetValue("relgrp")
	m.groupForm.path.SetValue("sub")
	if _, cmd := m.submitGroupForm(); cmd == nil {
		t.Fatalf("group form should submit, err=%q", m.errBar.text)
	}
	groups, _ := m.store.Groups()
	for _, g := range groups {
		if g.Name == "relgrp" && !filepath.IsAbs(g.Path) {
			t.Fatalf("group path stored relative: %q", g.Path)
		}
	}
}

// TestEnterOnABrowsedDirectoryEndsTheWalk pins the key that gets out of the
// listing. The directory is the card's last field, so the enter after picking
// one is meant to create the session; before this it reopened the listing one
// level down and the card could not be submitted from the field at all.
func TestEnterOnABrowsedDirectoryEndsTheWalk(t *testing.T) {
	root := setupCompletionDir(t)
	m := buildModel(t)
	m.openForm()
	focusFormField(t, m, fieldDir, "fieldDir")
	m.form.dir.SetValue(root + "/")
	m.pathSugg.browse(m.form.dir.Value())

	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got, want := m.form.dir.Value(), filepath.Join(root, "alpha"); got != want {
		t.Fatalf("dir = %q, want %q", got, want)
	}
	if m.pathSugg.showing() {
		t.Fatal("enter should close the listing, so the next enter creates the session")
	}
	if m.mode != modeForm {
		t.Fatalf("mode = %v, want the card still open", m.mode)
	}
}

// TestDirectoryIsTheLastFieldOnTheCard keeps the dropdown at the bottom of the
// card: a listing opened anywhere else pushes every field under it down, and
// the walk is the one thing on this card that grows.
func TestDirectoryIsTheLastFieldOnTheCard(t *testing.T) {
	if fieldDir != fieldCount-1 {
		t.Fatalf("fieldDir = %d, want the last field before fieldCount = %d", fieldDir, fieldCount)
	}
	m := buildModel(t)
	m.openForm()
	focusFormField(t, m, fieldDir, "fieldDir")
	m.formFocus(1)
	if m.form.focus != fieldTool {
		t.Fatalf("focus after dir = %d, want the card to wrap to the tool field", m.form.focus)
	}
}
