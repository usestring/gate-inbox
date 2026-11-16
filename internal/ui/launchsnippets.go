package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/snippets"
	"github.com/usestring/gate-inbox/internal/store"
)

func (m *Model) loadLaunchSnippets() {
	if m.configDir() == "" {
		m.launchSnips, m.launchSnipErr = snippets.Set{}, "launch snippets file is unavailable"
		return
	}
	set, err := snippets.LoadLaunch(m.configDir())
	m.launchSnips, m.launchSnipErr = set, ""
	if err != nil {
		m.launchSnipErr = err.Error()
	}
}

func (m *Model) openLaunchSnippets() {
	m.clearQuickBlockedNotice()
	m.quick.launch = true
	m.quick.suggestions = nil
	m.autoSuggestSeq++
	m.loadLaunchSnippets()
}

func (m *Model) handleLaunchSnippetKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	context := keymap.ContextList
	if m.quick.fromFocus {
		context = keymap.ContextFocus
	}
	if action, bound := m.action(context, msg); bound && action == keymap.QuickInput {
		m.quick.launch = false
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.quick = quickState{}
		m.clearQuickBlockedNotice()
		return m, nil
	case "ctrl+e":
		if m.configDir() == "" {
			m.errBar.text = "launch snippets file is unavailable"
			return m, nil
		}
		return m.launchEditor(snippets.LaunchPath(m.configDir()))
	}
	snip, ok := m.launchSnips.Get(msg.String())
	if !ok {
		return m, nil
	}
	m.quick = quickState{}
	return m.launchSnippet(snip)
}

func (m *Model) launchSnippet(snip snippets.Snippet) (tea.Model, tea.Cmd) {
	toolName := m.defaultTool()
	if toolName == "" {
		m.errBar.text = m.noCLIText()
		return m, nil
	}
	group := m.contextGroup()
	dir, ok := resolveExistingDir("", m.groupDefaultDir(group))
	if !ok {
		m.errBar.text = "working directory does not exist: " + dir
		return m, nil
	}
	name := m.derivedSessionName(dir, toolName)
	id, err := m.spawnSessionAs(toolName, "", name, dir, group, snip.Text, false, store.SourceDerived)
	if err != nil {
		m.reportLaunchError(err)
		return m, nil
	}
	m.statusFilter = statusFilterAll
	m.toolFilter = ""
	m.errBar.text = ""
	return m.landInNewSession(id)
}

func (m *Model) launchSnippetHelpSection() helpSection {
	rows := []helpRow{note("press the hotkey-menu key twice, then a launch key; esc closes the menu.")}
	for _, snip := range m.launchSnips.Snippets {
		rows = append(rows, lit(snip.Key, "launch "+snip.Title()))
	}
	rows = append(rows, note("edit launches with ctrl+e in the launch menu: "+snippets.LaunchPath(m.configDir())))
	if m.launchSnipErr != "" {
		rows = append(rows, note("✕ "+m.launchSnipErr))
	}
	for _, problem := range m.launchSnips.Problems {
		rows = append(rows, note("✕ "+problem))
	}
	return helpSection{title: "launch snippets", rows: rows}
}
