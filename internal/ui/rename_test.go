// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRenameGroupCascades(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()

	if err := m.store.CreateGroup("old/inner", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "kid", dir, "old/inner")

	for i, r := range m.rows {
		if r.isGroup && r.group == "old" {
			m.cursor = i
		}
	}
	m.collapsed["old"] = true
	m.rebuildRows()
	m.openRename()
	if m.groupForm.editing != "old" {
		t.Fatalf("edit target wrong: %q", m.groupForm.editing)
	}
	m.groupForm.name.SetValue("fresh")
	_, cmd := m.handleGroupFormKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.applyCmd(t, cmd)

	kid := m.sessionRows()
	if len(kid) != 0 {
		t.Fatalf("fresh should stay collapsed after rename, got %d sessions", len(kid))
	}
	if !m.collapsed["fresh"] || m.collapsed["old"] {
		t.Fatalf("collapse state should follow rename: %v", m.collapsed)
	}
	m.collapsed["fresh"] = false
	m.rebuildRows()
	sessions := m.sessionRows()
	if len(sessions) != 1 || sessions[0].Group != "fresh/inner" {
		t.Fatalf("session group should cascade to fresh/inner, got %+v", sessions)
	}
	groups, _ := m.store.Groups()
	for _, g := range groups {
		if strings.HasPrefix(g.Name, "old") {
			t.Fatalf("old group path survived rename: %v", groups)
		}
	}
}

func TestRenameSession(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "before", t.TempDir(), "")
	m.selectSessionRow(t, "before")
	id := m.sessionRows()[0].ID
	if err := m.store.SetAgentSessionID(id, "conv-keep"); err != nil {
		t.Fatalf("set agent id: %v", err)
	}
	m.openRename()
	m.rename.input.SetValue("after")
	_, cmd := m.handleRenameKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.applyCmd(t, cmd)
	got := m.sessionRows()[0]
	if got.Name != "after" {
		t.Fatalf("rename failed: %+v", got)
	}
	stored, err := m.store.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.AgentSessionID != "conv-keep" {
		t.Fatalf("name-only rename wiped agent session id: %q", stored.AgentSessionID)
	}
	if stored.Tool != "claude" {
		t.Fatalf("name-only rename changed tool: %q", stored.Tool)
	}
}

func TestRenameSessionWithoutAWorktreeIsUnchanged(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "plain", t.TempDir(), "")
	m.selectSessionRow(t, "plain")
	spawned := m.sessionRows()[0]

	m.openRename()
	m.rename.input.SetValue("still-plain")
	_, cmd := m.handleRenameKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.applyCmd(t, cmd)

	stored, err := m.store.Get(spawned.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Name != "still-plain" || stored.Cwd != spawned.Cwd {
		t.Fatalf("shared-directory session should rename in place: %+v", stored)
	}
}

func TestRenameSessionChangesTool(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "swapped", t.TempDir(), "")
	m.selectSessionRow(t, "swapped")
	start := m.sessionRows()[0]
	if start.Tool != "claude" {
		t.Fatalf("setup tool = %q want claude", start.Tool)
	}
	if err := m.store.SetAgentSessionID(start.ID, "conv-1"); err != nil {
		t.Fatalf("set agent id: %v", err)
	}
	m.openRename()
	if m.renameTool() != "claude" {
		t.Fatalf("rename tool start = %q want claude", m.renameTool())
	}
	if len(m.rename.toolNames) < 2 {
		t.Fatalf("need at least 2 tools to cycle, got %v", m.rename.toolNames)
	}
	m.handleRenameKey(tea.KeyPressMsg{Code: tea.KeyTab})
	wantTool := m.rename.toolNames[1]
	if m.renameTool() != wantTool {
		t.Fatalf("after tab tool = %q want %q", m.renameTool(), wantTool)
	}
	_, cmd := m.handleRenameKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.applyCmd(t, cmd)
	got := m.sessionRows()[0]
	if got.Tool != wantTool {
		t.Fatalf("tool after save = %q want %q", got.Tool, wantTool)
	}
	if got.AgentSessionID != "" {
		t.Fatalf("agent session id should clear on tool change, got %q", got.AgentSessionID)
	}
	stored, err := m.store.Get(got.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Tool != wantTool || stored.AgentSessionID != "" {
		t.Fatalf("store after save: tool=%q agent=%q", stored.Tool, stored.AgentSessionID)
	}
}

func TestEditGroupRenamesAndSetsPath(t *testing.T) {
	m := buildModel(t)
	oldDir := t.TempDir()
	newDir := t.TempDir()
	if err := m.store.CreateGroup("backend", oldDir); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	for i, row := range m.rows {
		if row.isGroup && row.group == "backend" {
			m.cursor = i
		}
	}

	m.openRename()
	if m.mode != modeGroupForm || m.groupForm.editing != "backend" {
		t.Fatalf("edit group should open, mode = %v editing = %q", m.mode, m.groupForm.editing)
	}
	if m.groupForm.path.Value() != oldDir {
		t.Fatalf("path prefill = %q want %q", m.groupForm.path.Value(), oldDir)
	}
	m.groupForm.name.SetValue("platform")
	m.groupForm.path.SetValue(newDir)
	if _, _ = m.submitGroupForm(); m.errBar.text != "" {
		t.Fatalf("submit: %q", m.errBar.text)
	}
	m.applyCmd(t, m.refreshCmd())

	if m.groupPaths["platform"] != newDir {
		t.Fatalf("platform path = %q want %q", m.groupPaths["platform"], newDir)
	}
	if _, exists := m.groupPaths["backend"]; exists {
		t.Fatal("old group name should be gone")
	}
}

func TestEditGroupRejectsMissingPath(t *testing.T) {
	m := buildModel(t)
	if err := m.store.CreateGroup("backend", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	for i, row := range m.rows {
		if row.isGroup && row.group == "backend" {
			m.cursor = i
		}
	}
	m.openRename()
	m.groupForm.path.SetValue("/nope/definitely/missing")
	if _, _ = m.submitGroupForm(); m.errBar.text == "" {
		t.Fatal("missing path should be rejected")
	}
	if m.mode != modeGroupForm {
		t.Fatal("card should stay open on error")
	}
}

func TestGroupPathNeverEmpty(t *testing.T) {
	m := buildModel(t)
	m.openGroupForm()
	if m.groupForm.path.Value() == "" {
		t.Fatal("group form path should prefill with a resolved directory")
	}
	m.groupForm.name.SetValue("zone")
	m.groupForm.path.SetValue("")
	if _, _ = m.submitGroupForm(); m.errBar.text != "" {
		t.Fatalf("submit: %q", m.errBar.text)
	}
	m.applyCmd(t, m.refreshCmd())
	if m.groupPaths["zone"] == "" {
		t.Fatal("created group should get a resolved default path, not empty")
	}

	for i, row := range m.rows {
		if row.isGroup && row.group == "zone" {
			m.cursor = i
		}
	}
	m.openRename()
	if m.groupForm.path.Value() == "" {
		t.Fatal("edit card should prefill the path")
	}
	m.groupForm.path.SetValue("")
	if _, _ = m.submitGroupForm(); m.errBar.text != "" {
		t.Fatalf("submit: %q", m.errBar.text)
	}
	m.applyCmd(t, m.refreshCmd())
	if m.groupPaths["zone"] == "" {
		t.Fatal("edited group should keep a resolved path when cleared")
	}
}

func TestRenameAgentToShellWithChildrenRefused(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "coder", dir, "backend")
	m.selectSessionRow(t, "coder")
	spawnTerminal(t, m)
	m.selectSessionRow(t, "coder")
	m.openRename()
	m.rename.input.SetValue("renamed")
	for i, name := range m.rename.toolNames {
		if name == "terminal" {
			m.rename.toolIndex = i
		}
	}
	_, cmd := m.handleRenameKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.applyCmd(t, cmd)
	if m.errBar.text == "" {
		t.Fatal("expected refuse")
	}
	got, _ := m.store.Get(m.sessionRows()[0].ID)
	if m.isShell(got.Tool) {
		t.Fatalf("tool became %q", got.Tool)
	}
	if got.Name != "coder" {
		t.Fatalf("name became %q", got.Name)
	}
}

// Re-parenting from the edit card is the same move m makes, so the subtree
// and the sessions in it follow the group to its new parent.
func TestGroupEditReparentsSubtree(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("platform", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	if err := m.store.CreateGroup("backend", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	if err := m.store.CreateGroup("backend/api", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	createSession(t, m, "kid", dir, "backend/api")
	m.selectGroupRow(t, "backend")

	m.openRename()
	// The group being edited cannot be its own parent, so its subtree is
	// not on offer.
	for _, opt := range m.form.groups {
		if opt.path == "backend" || opt.path == "backend/api" {
			t.Fatalf("parent picker offered the edited subtree: %+v", m.form.groups)
		}
	}
	for i, opt := range m.form.groups {
		if opt.path == "platform" {
			m.form.groupIndex = i
		}
	}
	if _, _ = m.submitGroupForm(); m.errBar.text != "" {
		t.Fatalf("submit: %q", m.errBar.text)
	}
	m.applyCmd(t, m.refreshCmd())

	if m.groupPaths["platform/backend"] != dir {
		t.Fatalf("group should sit under platform, paths = %v", m.groupPaths)
	}
	if _, exists := m.groupPaths["backend"]; exists {
		t.Fatalf("old path survived the move: %v", m.groupPaths)
	}
	sessions := m.sessionRows()
	if len(sessions) != 1 || sessions[0].Group != "platform/backend/api" {
		t.Fatalf("session should follow the subtree, got %+v", sessions)
	}
}

// An archived group is left out of the parent picker, so editing one of its
// children has to put it back: opening on the root instead would move the
// group there on the next save without anyone asking for it.
func TestGroupEditKeepsAnArchivedParent(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	if err := m.store.CreateGroup("shelf/live", dir); err != nil {
		t.Fatalf("group: %v", err)
	}
	if err := m.store.SetGroupArchived("shelf", true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	m.showArchived = true
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "shelf/live")

	m.openRename()
	if got := m.selectedGroupPath(); got != "shelf" {
		t.Fatalf("parent picker opened on %q, want shelf", got)
	}
	if _, _ = m.submitGroupForm(); m.errBar.text != "" {
		t.Fatalf("submit: %q", m.errBar.text)
	}
	m.applyCmd(t, m.refreshCmd())
	if _, exists := m.groupPaths["live"]; exists {
		t.Fatalf("group escaped to the root: %v", m.groupPaths)
	}
	if m.groupPaths["shelf/live"] != dir {
		t.Fatalf("group should have stayed put, paths = %v", m.groupPaths)
	}
}
