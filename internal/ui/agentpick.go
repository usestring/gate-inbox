package ui

import (
	"context"
	"maps"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/status"
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
	newSessionAgentAuto    = "auto"
)

// newSessionAgentModes is the setting's cycle order.
var newSessionAgentModes = []string{newSessionAgentAsk, newSessionAgentLast, newSessionAgentDefault, newSessionAgentAuto}

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
	case newSessionAgentAuto:
		return m.startAutoRoute()
	}
	m.openAgentPick()
	return m, nil
}

type autoRouteMsg struct {
	name     string
	names    []string
	active   map[string]int
	group    string
	row      treeRow
	selected bool
	mode     string
	err      error
}

func (m *Model) startAutoRoute() (tea.Model, tea.Cmd) {
	if m.autoRouting {
		return m, nil
	}
	names := m.enabledToolNames()
	mode, err := accounts.Mode(m.store)
	if err != nil {
		m.openAgentPick()
		m.errBar.text = "account routing unavailable: choose a CLI"
		return m, nil
	}
	if mode == accounts.Smart {
		eligible := names[:0]
		for _, name := range names {
			if m.cfg.Tools[name].AccountEnv == "" {
				eligible = append(eligible, name)
			}
		}
		names = eligible
	}
	if len(names) == 0 {
		m.openAgentPick()
		m.errBar.text = "quota unavailable: choose a CLI"
		return m, nil
	}
	row, selected := m.selectedRow()
	return m, m.autoRouteCmd(autoRouteMsg{names: names, active: m.activeByTool(), group: m.contextGroup(), row: row, selected: selected, mode: mode})
}

// activeByTool counts the sessions each CLI already has in flight, which the
// score reserves quota for.
func (m *Model) activeByTool() map[string]int {
	active := map[string]int{}
	for _, session := range m.sessions {
		if !session.Archived && (session.Status == status.Working || session.Status == status.Starting || session.Status == status.Waiting) {
			active[session.Tool]++
		}
	}
	return active
}

// autoRouteCmd reads quota and scores req.names against req.active, handing
// the rest of req back with the choice.
func (m *Model) autoRouteCmd(req autoRouteMsg) tea.Cmd {
	m.autoRouting = true
	router := m.autoRouter
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		msg := req
		msg.name, msg.err = router.Choose(ctx, req.names, req.active, time.Now())
		return msg
	}
}

func (m *Model) finishAutoRoute(msg autoRouteMsg) (tea.Model, tea.Cmd) {
	if !m.autoRouting {
		return m, nil
	}
	m.autoRouting = false
	if m.mode != modeList && m.mode != modeFocus {
		return m, nil
	}
	// Checked before anything pins to the captured group, so neither a launch
	// nor a fallback picker can reach a group renamed or archived meanwhile.
	if !m.groupStillOpen(msg.group) {
		m.openAgentPick()
		m.errBar.text = "group changed: choose a CLI"
		return m, nil
	}
	if msg.err != nil || msg.name == "" {
		m.openPinnedAgentPick(msg)
		m.errBar.text = "quota unavailable: choose a CLI"
		return m, nil
	}
	// The quota was scored under the routing mode read when n was pressed; a
	// mode changed in settings since then may route this CLI through an
	// account that was never scored.
	if mode, err := accounts.Mode(m.store); err != nil || mode != msg.mode {
		m.openPinnedAgentPick(msg)
		m.errBar.text = "account routing changed: choose a CLI"
		return m, nil
	}
	// Work that started during the read changes the reserve the score holds
	// back, so the choice is made again against the counts as they are now.
	// The readings are cached, so the second pass does not wait on quota.
	if active := m.activeByTool(); !maps.Equal(active, msg.active) {
		msg.active = active
		return m, m.autoRouteCmd(msg)
	}
	for _, name := range m.enabledToolNames() {
		if name == msg.name {
			return m.spawnInstantIn(name, msg.group)
		}
	}
	m.openPinnedAgentPick(msg)
	m.errBar.text = "chosen CLI is disabled: choose another"
	return m, nil
}

// groupStillOpen reports whether group can still take a new session: the top
// level, or a group that exists and is not archived. The store is read rather
// than the last poll, because a rename or archive finished during the quota
// read may not have reached the model yet.
func (m *Model) groupStillOpen(group string) bool {
	if group == "" {
		return true
	}
	groups, err := m.store.Groups()
	if err != nil {
		return false
	}
	names := make([]string, len(groups))
	archived := make(map[string]bool, len(groups))
	for i, g := range groups {
		names[i] = g.Name
		archived[g.Name] = g.Archived
	}
	return groupClosure(names, m.sessions)[group] && !store.EffectivelyArchived(archived, group)
}

func (m *Model) openPinnedAgentPick(msg autoRouteMsg) {
	m.openAgentPick()
	if m.mode == modeAgentPick {
		m.agentPick.group, m.agentPick.pinned = msg.group, true
		m.agentPick.row, m.agentPick.rowSelected = msg.row, msg.selected
	}
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
	// group and row pin the launch to what an Auto route captured when n was
	// pressed, so a fallback picker does not follow a cursor moved meanwhile.
	group       string
	row         treeRow
	rowSelected bool
	pinned      bool
}

func (m *Model) openAgentPick() {
	names := m.pickerNames()
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
		m.mode = modeList
		return m, nil
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

// submitAgentPick starts the agent the box is showing, or a plain terminal
// when that is the pick -- the same shell T opens. A filter that matches
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
	if m.isShell(name) {
		if m.agentPick.pinned {
			return m.openPinnedTerminal()
		}
		return m.openTerminal()
	}
	if m.agentPick.pinned {
		return m.spawnInstantIn(name, m.agentPick.group)
	}
	return m.spawnInstant(name)
}

// openPinnedTerminal opens the fallback shell beside the row captured when n
// was pressed, read fresh so a moved pane directory is followed. A session
// closed or archived meanwhile leaves only its group to open in.
func (m *Model) openPinnedTerminal() (tea.Model, tea.Cmd) {
	row := m.agentPick.row
	if !m.agentPick.rowSelected {
		return m.openTerminalIn(m.agentPick.group)
	}
	if !row.isGroup {
		sess, ok := m.sessionByID(row.sess.ID)
		if !ok || sess.Archived {
			return m.openTerminalIn(m.agentPick.group)
		}
		row.sess = sess
	}
	return m.openTerminalAt(row, true)
}
