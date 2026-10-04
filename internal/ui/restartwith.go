package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/restartpresets"
	"github.com/usestring/gate-inbox/internal/store"
)

// restartWithState holds the restart-with-flags picker: the session it will
// restart and the cursor into the loaded presets.
type restartWithState struct {
	sess  store.Session
	index int
}

// loadRestartFlags reads the file into the model. The set changes only at
// startup or when Settings closes, the way snippets do, so a file edit
// cannot change what a key does between two presses without the operator
// returning through that explicit boundary.
func (m *Model) loadRestartFlags() {
	dir := m.configDir()
	if dir == "" {
		return
	}
	set, err := restartpresets.Load(dir)
	m.restartFlags, m.restartFlagErr = set, ""
	if err != nil {
		m.restartFlagErr = err.Error()
	}
}

func (m *Model) openRestartFlagEditor() (tea.Model, tea.Cmd) {
	dir := m.configDir()
	if dir == "" {
		m.errBar.text = "restart flags file is unavailable"
		return m, nil
	}
	return m.launchEditor(restartpresets.Path(dir))
}

// openRestartWith opens the picker for the session under the cursor. A group
// has no pane to restart, and a file that would not load leaves no presets
// to pick from.
func (m *Model) openRestartWith() {
	entry, ok := m.selectedRow()
	if !ok {
		return
	}
	if entry.isGroup {
		m.errBar.text = "restart applies to a session; pick one under " + displayGroup(entry.group)
		return
	}
	if _, ok := m.cfg.Tools[entry.sess.Tool]; !ok {
		m.errBar.text = fmt.Sprintf("tool %s is no longer configured", entry.sess.Tool)
		return
	}
	if m.restartFlagErr != "" {
		m.errBar.text = "restart flags could not be read: " + m.restartFlagErr
		return
	}
	if len(m.restartFlags.Presets) == 0 {
		m.errBar.text = "no restart flags configured"
		if len(m.restartFlags.Problems) > 0 {
			m.errBar.text += ": " + strings.Join(m.restartFlags.Problems, "; ")
		}
		return
	}
	m.restartWith = restartWithState{sess: entry.sess, index: 0}
	m.errBar.text = ""
	m.mode = modeRestartWith
}

func (m *Model) restartWithPreset() *restartpresets.Preset {
	if len(m.restartFlags.Presets) == 0 {
		return nil
	}
	if m.restartWith.index < 0 || m.restartWith.index >= len(m.restartFlags.Presets) {
		return nil
	}
	return &m.restartFlags.Presets[m.restartWith.index]
}

func (m *Model) handleRestartWithKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.errBar.text = ""
		return m, nil
	case "up", "k":
		n := len(m.restartFlags.Presets)
		if n > 0 {
			m.restartWith.index = (m.restartWith.index - 1 + n) % n
		}
		return m, nil
	case "down", "j", "tab":
		n := len(m.restartFlags.Presets)
		if n > 0 {
			m.restartWith.index = (m.restartWith.index + 1) % n
		}
		return m, nil
	case "enter":
		preset := m.restartWithPreset()
		if preset == nil {
			return m, nil
		}
		return m.submitRestartWith(*preset)
	}
	for _, preset := range m.restartFlags.Presets {
		if msg.String() == preset.Key {
			return m.submitRestartWith(preset)
		}
	}
	return m, nil
}

// submitRestartWith leaves the picker for the restart confirm, carrying the
// preset's flags. The confirm is the destructive boundary: it names the
// session and the flags, and y means kill the pane and start over.
func (m *Model) submitRestartWith(preset restartpresets.Preset) (tea.Model, tea.Cmd) {
	sess, err := m.store.Get(m.restartWith.sess.ID)
	if err != nil {
		m.mode = modeList
		m.errBar.text = err.Error()
		return m, nil
	}
	label := fmt.Sprintf("restart %s with %s? ends the running agent and leaves its conversation behind.", sess.Name, preset.Args)
	if !m.tmux.Exists(sess.ID) {
		label = fmt.Sprintf("restart %s with %s? its current conversation is left behind.", sess.Name, preset.Args)
	}
	m.confirm = confirmTarget{
		action:      actionRestart,
		sessions:    []store.Session{sess},
		label:       label,
		restartArgs: strings.TrimSpace(preset.Args),
	}
	m.mode = modeConfirmDelete
	return m, nil
}

func (m *Model) viewRestartWith() string {
	var b strings.Builder
	b.WriteString("  restart  " + valueStyle.Render(m.restartWith.sess.Name) + "\n")
	b.WriteString("  with extra flags; pick one\n")
	hint := [][2]string{{"↑↓", "move"}, {"↵", "restart"}, {"esc", "back"}}
	room := m.height - 9
	room -= strings.Count(legendInline(hint, cardInnerWidth(m.cardWidth())), "\n")
	if len(m.restartFlags.Problems) > 0 {
		room--
	}
	if m.errBar.text != "" {
		room -= 2
	}
	start, end := scrollWindow(len(m.restartFlags.Presets), m.restartWith.index, max(3, room))
	if start > 0 {
		b.WriteString("  ↑ more\n")
	}
	for i := start; i < end; i++ {
		preset := m.restartFlags.Presets[i]
		marker := "  "
		if m.restartWith.index == i {
			marker = "❯ "
		}
		b.WriteString(marker + preset.Key + "  " + valueStyle.Render(preset.Title()) + "\n")
	}
	if end < len(m.restartFlags.Presets) {
		b.WriteString("  ↓ more\n")
	}
	if len(m.restartFlags.Problems) > 0 {
		b.WriteString("  " + mutedStyle.Render(strings.Join(m.restartFlags.Problems, "; ")) + "\n")
	}
	return m.cardFlex("↻ Restart with flags", b.String(), hint)
}
