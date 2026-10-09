package ui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/store"
)

func visibleSessionIDs(m *Model) []string {
	var ids []string
	for _, sess := range m.sessionRows() {
		ids = append(ids, sess.ID)
	}
	return ids
}

func pressReorder(t *testing.T, m *Model, key rune) *Model {
	t.Helper()
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: key, Text: string(key)})
	return updated.(*Model)
}

// A poll pass listed the board before the swap and delivers after it: the
// stale listing must not put the moved row back, and the next press must
// keep moving it rather than undo the first.
func TestReorderSurvivesAStalePollListing(t *testing.T) {
	m := buildModel(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := m.store.CreateSession(store.Session{ID: id, Name: "name-" + id, Tool: "claude", Cwd: "/tmp", Status: "idle"}); err != nil {
			t.Fatalf("create session %q: %v", id, err)
		}
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, "name-c")

	stale := m.refreshCmd()()
	m = pressReorder(t, m, 'K')
	updated, _ := m.Update(stale)
	m = updated.(*Model)
	if got, want := visibleSessionIDs(m), []string{"a", "c", "b"}; !slices.Equal(got, want) {
		t.Fatalf("visible order after a stale poll = %v want %v", got, want)
	}
	if sess, ok := m.selected(); !ok || sess.ID != "c" {
		t.Fatalf("cursor should stay on the moved row, got %+v", sess)
	}

	m = pressReorder(t, m, 'K')
	if got, want := listSessionIDs(t, m.store), []string{"c", "a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("stored order after a second K = %v want %v", got, want)
	}
	m.applyCmd(t, m.refreshCmd())
	if got, want := visibleSessionIDs(m), []string{"c", "a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("visible order after a fresh poll = %v want %v", got, want)
	}
}

func TestGroupReorderSurvivesAStalePollListing(t *testing.T) {
	m := buildModel(t)
	for _, group := range []string{"alpha", "beta", "gamma"} {
		if err := m.store.CreateGroup(group, ""); err != nil {
			t.Fatalf("create group %q: %v", group, err)
		}
		if err := m.store.CreateSession(store.Session{ID: group, Name: group + "-s", Tool: "claude", Cwd: "/tmp", Group: group, Status: "idle"}); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "gamma")

	stale := m.refreshCmd()()
	m = pressReorder(t, m, 'K')
	updated, _ := m.Update(stale)
	m = updated.(*Model)
	if got, want := m.groupRowPaths(), []string{"alpha", "gamma", "beta"}; !slices.Equal(got, want) {
		t.Fatalf("group order after a stale poll = %v want %v", got, want)
	}
	m = pressReorder(t, m, 'K')
	m.applyCmd(t, m.refreshCmd())
	if got, want := m.groupRowPaths(), []string{"gamma", "alpha", "beta"}; !slices.Equal(got, want) {
		t.Fatalf("group order after a second K and a fresh poll = %v want %v", got, want)
	}
}
