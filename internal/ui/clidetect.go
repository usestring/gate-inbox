package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/config"
)

// The new-session pickers offer every configured agent CLI, and the defaults
// configure claude, codex and opencode whether or not this machine has them.
// So each one's command is looked up on PATH, and a CLI that is not there is
// still drawn -- grayed, so the operator can see it is an option the board
// knows -- but cannot be picked, and nothing defaults to it. The lookup runs
// once at startup and again on refresh installed CLIs, after an install.

// cliDetectMsg carries which configured agent CLIs were not found on PATH.
type cliDetectMsg struct {
	missing map[string]bool
	// report says the result in the error bar: the operator asked for it.
	report bool
}

// detectCLIsCmd looks every configured agent CLI up off the event loop: a
// lookup on WSL can walk the Windows half of PATH.
func (m *Model) detectCLIsCmd(report bool) tea.Cmd {
	commands := make(map[string]string, len(m.cfg.Tools))
	for _, name := range sortedToolNames(m.cfg) {
		commands[name] = m.cfg.Tools[name].Command
	}
	return func() tea.Msg {
		missing := map[string]bool{}
		for name, command := range commands {
			if config.CheckInstalled(command) != nil {
				missing[name] = true
			}
		}
		return cliDetectMsg{missing: missing, report: report}
	}
}

func (m *Model) finishCLIDetect(msg cliDetectMsg) {
	m.missingTools = msg.missing
	if m.mode == modeForm && m.toolMissing(m.selectedToolName()) {
		if matches := m.formToolMatches(); len(matches) > 0 {
			m.selectTool(matches[0])
		}
	}
	if !msg.report {
		return
	}
	var found, missing []string
	for _, name := range sortedToolNames(m.cfg) {
		if msg.missing[name] {
			missing = append(missing, name)
		} else {
			found = append(found, name)
		}
	}
	text := "CLIs installed: " + listOrNone(found)
	if len(missing) > 0 {
		text += " · not found: " + strings.Join(missing, ", ")
	}
	m.reportDone(text)
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// toolMissing reports whether the last lookup failed to find name. Before
// the first lookup answers, every CLI counts as present.
func (m *Model) toolMissing(name string) bool {
	return m.missingTools[name]
}

// installedOnly drops the CLIs the last lookup did not find.
func (m *Model) installedOnly(names []string) []string {
	if len(m.missingTools) == 0 {
		return names
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if !m.toolMissing(name) {
			out = append(out, name)
		}
	}
	return out
}

func firstOf(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// noMatchText is why a typed filter starts nothing: no CLI matches it, or
// only ones not on PATH.
func (m *Model) noMatchText(names []string, typed string) string {
	typed = strings.TrimSpace(typed)
	if hits := matchTools(names, typed); len(hits) > 0 {
		return hits[0] + " is not found on PATH: install it, then run refresh installed CLIs from quick actions (:)"
	}
	return "no CLI matches " + typed
}

// noCLIText is what a picker says when it has nothing it can start.
func (m *Model) noCLIText() string {
	names := m.pickerNames()
	if len(names) > 0 && len(m.installedOnly(names)) == 0 {
		return "no enabled CLI is installed: install one, then run refresh installed CLIs from quick actions (:)"
	}
	return "no CLIs enabled: open settings (s), then CLIs, to turn some on"
}
