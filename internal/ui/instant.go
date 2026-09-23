package ui

import (
	"path/filepath"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/store"
)

// spawnInstant starts an agent once the picker has named the CLI.
//
// Nothing else is asked. The group is the one under the cursor, the directory
// is what that group resolves to, and the name is derived from that directory
// until the agent's own conversation title replaces it. The form is still
// there, on its own key, for a spawn somebody wants to decide the rest of.
//
// The CLI is the one question, because it is the only answer that changes what
// gets launched and the only one an operator regularly wants different from
// last time; see agentpick.go for why it is asked in a box rather than taken
// from a setting.
//
// No first prompt means no rename directive either: the directive is a turn
// the agent has to spend before it can do anything, and an agent nobody has
// spoken to yet has nothing to name itself after. The title pass names the row
// once there is a conversation to read, which costs the agent nothing.
func (m *Model) spawnInstant(toolName string) (tea.Model, tea.Cmd) {
	if toolName == "" {
		m.errBar.text = "no CLIs enabled: open settings (s), then CLIs, to turn some on"
		return m, nil
	}
	group := m.contextGroup()
	dir, ok := resolveExistingDir("", m.groupDefaultDir(group))
	if !ok {
		m.errBar.text = "working directory does not exist: " + dir
		return m, nil
	}
	name := m.derivedSessionName(dir, toolName)
	id, err := m.spawnSessionAs(toolName, "", name, dir, group, "", false, store.SourceDerived)
	if err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	// A new session is starting, which the attention filter excludes, so the
	// row this keypress made would otherwise land off screen.
	m.statusFilter = statusFilterAll
	m.errBar.text = ""
	return m.landInNewSession(id)
}

// derivedSessionName is what an instant spawn wears until its conversation has
// a title: the working directory's own name, and the counter that tells a
// burst of them apart.
//
// The shape is adoption's, deliberately -- basename, then "-2", "-3" -- because
// that is the shape the store recognises as a name the manager made up, and a
// row whose name reads as derived is a row the title pass is allowed to rename.
func (m *Model) derivedSessionName(dir, toolName string) string {
	base := filepath.Base(dir)
	switch base {
	case "", ".", string(filepath.Separator):
		base = toolName
	}
	taken := make(map[string]bool, len(m.sessions))
	for _, sess := range m.sessions {
		taken[sess.Name] = true
	}
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := base + "-" + strconv.Itoa(n)
		if !taken[candidate] {
			return candidate
		}
	}
}
