package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/store"
)

// lastToolSetting remembers the CLI the last spawn ran, so the next one opens
// on it. It is deliberately not default_tool: that setting is a choice the
// operator made in settings and expects to stay put, while this one is a
// record of what they actually did.
const lastToolSetting = "last_tool"

// Asking every time is right for somebody who moves between CLIs and wrong
// for somebody who has one and answers the same box all day: for them n is
// two keys where one would do, every single time.
//
// So the question becomes a setting. It stays on ask, because a box that
// opens on the last CLI used is already one keystroke; what the other modes
// add is the choice to stop being asked, and which answer to stand in for
// the one nobody is giving any more -- the CLI the last spawn used, which
// follows what is actually being run, or the settings default, which does
// not move until it is moved.
//
// Nothing is lost by turning it off: ctrl+n still opens the full form, with
// its own CLI picker, for the spawn that wants a different one.
const (
	newSessionAgentSetting = "new_session_agent"
	// newSessionAgentAsk is the default: n opens the box.
	newSessionAgentAsk = "ask"
	// newSessionAgentLast skips the box and starts the CLI the last spawn
	// used.
	newSessionAgentLast = "last used"
	// newSessionAgentDefault skips the box and starts the settings default
	// tool.
	newSessionAgentDefault = "default tool"
)

// newSessionAgentModes is the setting's cycle order.
var newSessionAgentModes = []string{newSessionAgentAsk, newSessionAgentLast, newSessionAgentDefault}

func storedNewSessionAgent(st *store.Store) string {
	chosen, err := st.Setting(newSessionAgentSetting)
	if err != nil {
		return newSessionAgentAsk
	}
	return normalizeNewSessionAgent(chosen)
}

func normalizeNewSessionAgent(chosen string) string {
	for _, mode := range newSessionAgentModes {
		if chosen == mode {
			return mode
		}
	}
	return newSessionAgentAsk
}

// startNewSession is the n key: the box, or the agent the operator already
// said n should start.
//
// The automatic modes resolve through the same two readers the box opens on,
// so a CLI turned off in settings is not launched behind anybody's back --
// last used falls through to the default, and the default falls through to
// the first CLI still enabled.
func (m *Model) startNewSession() (tea.Model, tea.Cmd) {
	switch m.newSessionAgent {
	case newSessionAgentLast:
		return m.spawnInstant(m.lastTool())
	case newSessionAgentDefault:
		return m.spawnInstant(m.defaultTool())
	}
	m.openAgentPick()
	return m, nil
}

// agentPick is the one question n asks: which agent starts here.
//
// It is a text box rather than a picker because the answer is almost always
// already known -- the CLI the last spawn used -- and the box opens on it, so
// the whole interaction is n then enter. What makes it worth asking at all is
// the other case: a box is where "codex" can just be typed, without arrowing
// through a list or going to settings to move the default and back again.
//
// The value and the selection are the same thing. Typing filters and snaps the
// selection to the best match; the arrows cycle the selection and write it back
// into the box. So the box always shows what enter would start.
type agentPick struct {
	input textinput.Model
	names []string
	index int
	// fresh marks the text as put there by this program rather than typed:
	// the prefill, or a name an arrow key wrote. The first character typed
	// against fresh text replaces the whole of it, which is what makes a
	// prefilled box overridable without a backspace per character.
	fresh bool
}

func (m *Model) openAgentPick() {
	names := m.enabledToolNames()
	if len(names) == 0 {
		m.errBar.text = "no CLIs enabled: open settings (s), then CLIs, to turn some on"
		return
	}
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 40
	input.SetWidth(28)
	input.Focus()
	m.agentPick = agentPick{input: input, names: names, fresh: true}
	m.setAgentPick(m.lastTool())
	m.errBar.text = ""
	m.mode = modeAgentPick
}

// lastTool is the CLI the box opens on: the one the last spawn used while it
// is still enabled, and otherwise the settings default. A CLI turned off since
// it was last used is not offered back.
func (m *Model) lastTool() string {
	names := m.enabledToolNames()
	if len(names) == 0 {
		return ""
	}
	last, err := m.store.Setting(lastToolSetting)
	if err != nil {
		m.errBar.text = "reading last tool setting: " + err.Error()
		return m.defaultTool()
	}
	for _, name := range names {
		if name == last {
			return last
		}
	}
	return m.defaultTool()
}

// rememberTool records the CLI a spawn used, so the next box opens on it.
// A store that refuses is surfaced and nothing more: a session that launched
// is not unlaunched over a preference that failed to stick.
func (m *Model) rememberTool(name string) {
	if name == "" || m.store == nil {
		return
	}
	if err := m.store.SetSetting(lastToolSetting, name); err != nil {
		m.errBar.text = "remembering the last CLI: " + err.Error()
	}
}

// selectAgentPick moves the selection onto a name, leaving it where it was
// when the name is not one of the CLIs on offer.
func (m *Model) selectAgentPick(name string) {
	for i, candidate := range m.agentPick.names {
		if candidate == name {
			m.agentPick.index = i
			return
		}
	}
}

// setAgentPick puts a name in the box and on the selection together, and
// leaves the text fresh so it is still one keystroke from being replaced.
func (m *Model) setAgentPick(name string) {
	m.selectAgentPick(name)
	m.agentPick.input.SetValue(name)
	m.agentPick.input.SetCursor(len(name))
	m.agentPick.fresh = true
}

func (m *Model) agentPickName() string {
	if m.agentPick.index >= 0 && m.agentPick.index < len(m.agentPick.names) {
		return m.agentPick.names[m.agentPick.index]
	}
	return ""
}

// agentPickMatches is the CLIs the typed text still allows -- and every CLI
// while the box still holds text this program put there.
//
// Fresh text is a value, not a filter. Treating it as one would narrow the box
// to the very name it opened on: the alternatives would be invisible in the
// only state the box is ever opened in, which is the state they exist for, and
// the arrows would cycle a list of one and appear dead.
func (m *Model) agentPickMatches() []string {
	if m.agentPick.fresh {
		return m.agentPick.names
	}
	return matchTools(m.agentPick.names, m.agentPick.input.Value())
}

// snapAgentPick keeps the selection on the best match for what has been typed.
// An empty box selects nothing new -- backspacing to empty means they are
// still deciding, not that they asked for the first CLI in the list.
func (m *Model) snapAgentPick() {
	if strings.TrimSpace(m.agentPick.input.Value()) == "" {
		return
	}
	if matches := m.agentPickMatches(); len(matches) > 0 {
		m.selectAgentPick(matches[0])
	}
}

// cycleAgentPick steps the selection through the CLIs the filter allows,
// wrapping at the ends, and writes the new name into the box.
func (m *Model) cycleAgentPick(delta int) {
	matches := m.agentPickMatches()
	if len(matches) == 0 {
		matches = m.agentPick.names
	}
	if len(matches) == 0 {
		return
	}
	at := 0
	current := m.agentPickName()
	for i, name := range matches {
		if name == current {
			at = i
		}
	}
	m.setAgentPick(matches[(at+delta+len(matches))%len(matches)])
}

// replacesFreshText reports whether a keypress should wipe the prefill first.
// Printable text does, because typing over a selected value is what every
// other box that opens on a value does. So does a delete key: on text that
// reads as selected, backspace means "get rid of it", not "trim a character".
func replacesFreshText(msg tea.KeyPressMsg) (wipe, consume bool) {
	switch msg.String() {
	case "backspace", "delete", "ctrl+u", "ctrl+w":
		return true, true
	}
	if msg.Key().Text != "" {
		return true, false
	}
	return false, false
}

func (m *Model) handleAgentPickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.errBar.text = ""
		return m.cancelSpawnToGate()
	case "enter":
		return m.submitAgentPick()
	case "tab", "down", "right":
		m.cycleAgentPick(1)
		return m, nil
	case "shift+tab", "up", "left":
		m.cycleAgentPick(-1)
		return m, nil
	}
	if m.agentPick.fresh {
		if wipe, consume := replacesFreshText(msg); wipe {
			m.agentPick.input.SetValue("")
			m.agentPick.fresh = false
			if consume {
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.agentPick.input, cmd = m.agentPick.input.Update(msg)
	m.agentPick.fresh = false
	m.snapAgentPick()
	return m, cmd
}

// submitAgentPick starts the agent the box is showing. A filter that matches
// nothing refuses rather than launching whatever was selected before it was
// typed, so nobody gets an agent they did not name.
func (m *Model) submitAgentPick() (tea.Model, tea.Cmd) {
	typed := strings.TrimSpace(m.agentPick.input.Value())
	if typed != "" && len(m.agentPickMatches()) == 0 {
		m.errBar.text = "no CLI matches " + typed
		return m, nil
	}
	name := m.agentPickName()
	if name == "" {
		m.errBar.text = "no CLIs enabled: open settings (s), then CLIs, to turn some on"
		m.mode = modeList
		return m, nil
	}
	m.mode = modeList
	return m.spawnInstant(name)
}
