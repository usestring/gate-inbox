// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func (m *Model) openRename() {
	entry, ok := m.selectedRow()
	if !ok {
		return
	}
	// A group is edited in the card that created it rather than in this
	// form: name is one of four things it has, and the other three have
	// nowhere to go here.
	if entry.isGroup {
		m.editGroupRow(entry)
		return
	}
	input := textinput.New()
	input.CharLimit = 60
	input.Prompt = ""
	input.Focus()
	input.SetValue(entry.sess.Name)
	tools := sortedToolNames(m.cfg)
	shells := []string{}
	for _, name := range m.cfg.ToolNames() {
		if m.cfg.Tools[name].Shell {
			shells = append(shells, name)
		}
	}
	sort.Strings(shells)
	tools = append(tools, shells...)
	toolIndex := 0
	for i, name := range tools {
		if name == entry.sess.Tool {
			toolIndex = i
			break
		}
	}
	// Current tool missing from config (removed block): keep it selectable
	// so save does not silently reassign to the first configured tool.
	if len(tools) == 0 || tools[toolIndex] != entry.sess.Tool {
		tools = append([]string{entry.sess.Tool}, tools...)
		toolIndex = 0
	}
	m.rename = renameTarget{
		sessID:    entry.sess.ID,
		input:     input,
		toolNames: tools,
		toolIndex: toolIndex,
	}
	m.mode = modeRename
	m.errBar.text = ""
}

// editGroupRow opens the group card on the row under the cursor. Both r and
// alt+r mean "edit what is selected", and for a group that is this card.
func (m *Model) editGroupRow(entry treeRow) {
	if entry.isRoot() {
		m.errBar.text = "root is the top level, not a group to edit"
		return
	}
	m.openGroupEditForm(entry.group)
}

func (m *Model) handleRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		return m, nil
	case "tab":
		m.cycleRenameTool(1)
		return m, nil
	case "shift+tab":
		m.cycleRenameTool(-1)
		return m, nil
	case "enter":
		return m.applyRename()
	}
	var cmd tea.Cmd
	m.rename.input, cmd = m.rename.input.Update(msg)
	return m, cmd
}

func (m *Model) cycleRenameTool(delta int) {
	if len(m.rename.toolNames) == 0 {
		return
	}
	n := len(m.rename.toolNames)
	m.rename.toolIndex = (m.rename.toolIndex + delta + n) % n
}

func (m *Model) renameTool() string {
	if len(m.rename.toolNames) == 0 {
		return ""
	}
	return m.rename.toolNames[m.rename.toolIndex]
}

func (m *Model) applyRename() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.rename.input.Value())
	name = strings.ReplaceAll(name, "/", "-")
	if name == "" {
		m.errBar.text = "name cannot be empty"
		return m, nil
	}
	index := -1
	for i := range m.sessions {
		if m.sessions[i].ID == m.rename.sessID {
			index = i
			break
		}
	}
	tool := m.renameTool()
	prevTool := ""
	if index >= 0 {
		prevTool = m.sessions[index].Tool
	}
	toolChanged := tool != "" && tool != prevTool
	if toolChanged && m.isShell(tool) {
		kids, err := m.store.Children(m.rename.sessID)
		if err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
		if len(kids) > 0 {
			m.errBar.text = "move its terminals first"
			return m, nil
		}
	}
	if err := m.store.RenameSession(m.rename.sessID, name); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	if toolChanged {
		if err := m.store.UpdateTool(m.rename.sessID, tool); err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
	}
	if index >= 0 {
		m.sessions[index].Name = name
		if toolChanged {
			m.sessions[index].Tool = tool
			m.sessions[index].AgentSessionID = ""
		}
	}
	m.relabelSession(m.rename.sessID)
	m.rebuildRows()
	m.mode = modeList
	m.requestRefresh()
	return m, nil
}

// renameGroupLocally rewrites the in-memory tree right away, so the
// frames between saving and the poller's next refresh already show the
// new name and path instead of flashing the stale ones.
func (m *Model) renameGroupLocally(old, newPath, dir string) {
	moved := func(group string) (string, bool) {
		if group == old || strings.HasPrefix(group, old+"/") {
			return newPath + group[len(old):], true
		}
		return group, false
	}
	for i := range m.groups {
		m.groups[i], _ = moved(m.groups[i])
	}
	for i := range m.sessions {
		m.sessions[i].Group, _ = moved(m.sessions[i].Group)
	}
	groupPaths := make(map[string]string, len(m.groupPaths))
	for group, path := range m.groupPaths {
		group, _ = moved(group)
		groupPaths[group] = path
	}
	groupPaths[newPath] = dir
	m.groupPaths = groupPaths
	for group, folded := range m.collapsed {
		if renamed, ok := moved(group); ok {
			delete(m.collapsed, group)
			m.collapsed[renamed] = folded
		}
	}
	m.persistCollapsed()
}

// relabelSession refreshes one session's tmux status-bar label from the db.
func (m *Model) relabelSession(id string) {
	sess, err := m.store.Get(id)
	if err != nil {
		m.errBar.text = err.Error()
		return
	}
	if !m.tmux.Exists(id) {
		return
	}
	if err := m.tmux.SetLabel(id, sessionLabel(sess.Group, sess.Name)); err != nil {
		m.errBar.text = err.Error()
	}
}

// relabelSubtree refreshes labels for every session under a group path.
func (m *Model) relabelSubtree(path string) {
	sessions, err := m.store.SessionsInSubtree(path)
	if err != nil {
		m.errBar.text = err.Error()
		return
	}
	for _, sess := range sessions {
		m.relabelSession(sess.ID)
	}
}
