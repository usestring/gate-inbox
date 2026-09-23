// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/store"
)

// defaultTool is the CLI quick spawn launches: the settings choice when it
// is still enabled, else the first enabled tool. A store error still yields
// the fallback but is surfaced, never swallowed.
func (m *Model) defaultTool() string {
	names := m.enabledToolNames()
	if len(names) == 0 {
		return ""
	}
	chosen, err := m.store.Setting("default_tool")
	if err != nil {
		m.errBar.text = "reading default tool setting: " + err.Error()
		return names[0]
	}
	if chosen != "" {
		for _, name := range names {
			if name == chosen {
				return chosen
			}
		}
	}
	return names[0]
}

const ownLogin = "own login"

func (s settingsState) routingValue() string {
	if !s.poolAvailable {
		return "own subscription (no account pool to route onto)"
	}
	if s.accountRouting == accounts.Smart {
		return "smart routing"
	}
	return "own subscription"
}

// hiddenTools returns the set of CLI names the user turned off for new sessions.
func (m *Model) hiddenTools() map[string]bool {
	raw, err := m.store.Setting(hiddenToolsSetting)
	if err != nil {
		m.errBar.text = "reading hidden tools setting: " + err.Error()
		return nil
	}
	return parseHiddenTools(raw)
}

func parseHiddenTools(raw string) map[string]bool {
	if raw == "" {
		return nil
	}
	hidden := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name != "" {
			hidden[name] = true
		}
	}
	if len(hidden) == 0 {
		return nil
	}
	return hidden
}

func formatHiddenTools(hidden map[string]bool) string {
	if len(hidden) == 0 {
		return ""
	}
	names := make([]string, 0, len(hidden))
	for name, on := range hidden {
		if on {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// storedComfortableRows reads the persisted list density. Compact is the
// default; a stored "comfortable" choice gives every entry a second line.
func storedComfortableRows(st *store.Store) bool {
	chosen, err := st.Setting(listDensitySetting)
	if err != nil {
		return false
	}
	return chosen == "comfortable"
}

// enterFocuses reports which key opens a session where. Enter focuses the
// preview and A attaches full screen by default; a stored "attach" choice
// swaps the pair. Cached on the model because the footer reads it every
// frame.
func (m *Model) enterFocuses() bool {
	return m.focusOnEnter
}

// storedFocusOnEnter reads the persisted key choice. A read failure yields
// the default pairing.
func storedFocusOnEnter(st *store.Store) bool {
	chosen, err := st.Setting(focusKeySetting)
	if err != nil {
		return true
	}
	return chosen != "attach"
}

func (m *Model) openSettings() {
	if len(m.cfg.Tools) == 0 {
		m.errBar.text = "no tools configured"
		return
	}
	m.errBar.text = ""
	names, index := m.defaultToolSelection()
	routing, err := accounts.Mode(m.store)
	if err != nil {
		m.errBar.text = err.Error()
	}
	// A stored smart with nothing to route onto refuses every unnamed
	// launch; shown and saved as own, closing settings is the way out.
	pool := accounts.PoolAvailable()
	if !pool && routing == accounts.Smart {
		routing = accounts.Own
	}
	m.settings = settingsState{
		toolNames:       names,
		toolIndex:       index,
		accountRouting:  routing,
		poolAvailable:   pool,
		themeIndex:      themeIndex(current.Name),
		quickCloseSend:  m.quickCloseAfterSend(),
		enterFocuses:    m.enterFocuses(),
		comfortableRows: m.comfortableRows,
		layout:          normalizeLayout(m.layout),
		palette:         normalizePalette(m.palette),
		glyphs:          normalizeGlyphs(m.glyphs),
		archiveConfirm:  normalizeArchiveConfirm(m.archiveConfirm),
		listSort:        normalizeListSort(m.listSort),
		chrome:          normalizeChrome(m.chrome),
		leaveMode:       normalizeLeaveMode(m.leaveMode),
		newSessionAgent: normalizeNewSessionAgent(m.newSessionAgent),
		autoProceed:     m.autoProceed,
		backdropSync:    storedBackdrop(m.store) == backdropSync,
	}
	m.mode = modeSettings
}

func (m *Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settings.cliPicker {
		return m.handleCLIPickerKey(msg)
	}
	switch msg.String() {
	case "up", "k":
		m.settings.field = (m.settings.field + settingsFieldCount - 1) % settingsFieldCount
	case "down", "j":
		m.settings.field = (m.settings.field + 1) % settingsFieldCount
	case "left", "h":
		return m, m.cycleSetting(-1)
	case "right", "l":
		return m, m.cycleSetting(1)
	case "enter":
		switch m.settings.field {
		case settingsFieldSnippets:
			return m.openSnippetEditor()
		case settingsFieldCLIs:
			m.openCLIPicker()
			return m, nil
		case settingsFieldGuide:
			// Saved and closed first: the guide returns to the list rather
			// than to settings, so anything cycled on the way here would
			// otherwise be dropped.
			model, cmd := m.saveAndCloseSettings()
			m.openWelcome()
			return model, cmd
		}
		return m.saveAndCloseSettings()
	case "esc":
		return m.saveAndCloseSettings()
	}
	return m, nil
}

func (m *Model) saveAndCloseSettings() (tea.Model, tea.Cmd) {
	m.persistSettings()
	m.loadSnippets()
	if m.snipErr != "" {
		m.errBar.text = "snippets could not be read: " + m.snipErr
	}
	m.rebuildRows()
	m.mode = modeList
	return m, nil
}

func (m *Model) persistSettings() {
	if m.settings.accountRouting != "" {
		if err := m.store.SetSetting(store.AccountRoutingSetting, m.settings.accountRouting); err != nil {
			m.errBar.text = err.Error()
		}
	}
	if len(m.settings.toolNames) > 0 {
		if err := m.store.SetSetting("default_tool", m.settings.toolNames[m.settings.toolIndex]); err != nil {
			m.errBar.text = err.Error()
		}
	}
	if err := m.store.SetSetting(m.themeSettingKey(), themes[m.settings.themeIndex].Name); err != nil {
		m.errBar.text = err.Error()
	}
	backdrop := backdropInherit
	if m.settings.backdropSync {
		backdrop = backdropSync
	}
	if err := m.store.SetSetting(backdropSetting, backdrop); err != nil {
		m.errBar.text = err.Error()
	}
	quickClose := "stay"
	if m.settings.quickCloseSend {
		quickClose = "close"
	}
	if err := m.store.SetSetting(quickCloseSetting, quickClose); err != nil {
		m.errBar.text = err.Error()
	}
	focusKey := "focus"
	if !m.settings.enterFocuses {
		focusKey = "attach"
	}
	if err := m.store.SetSetting(focusKeySetting, focusKey); err != nil {
		m.errBar.text = err.Error()
	}
	density := "compact"
	if m.settings.comfortableRows {
		density = "comfortable"
	}
	if err := m.store.SetSetting(listDensitySetting, density); err != nil {
		m.errBar.text = err.Error()
	}
	if err := m.store.SetSetting(layoutSetting, normalizeLayout(m.settings.layout)); err != nil {
		m.errBar.text = err.Error()
	}
	if err := m.store.SetSetting(paletteSetting, normalizePalette(m.settings.palette)); err != nil {
		m.errBar.text = err.Error()
	}
	if err := m.store.SetSetting(glyphsSetting, normalizeGlyphs(m.settings.glyphs)); err != nil {
		m.errBar.text = err.Error()
	}
	if err := m.store.SetSetting(archiveConfirmSetting, normalizeArchiveConfirm(m.settings.archiveConfirm)); err != nil {
		m.errBar.text = err.Error()
	}
	if err := m.store.SetSetting(listSortSetting, normalizeListSort(m.settings.listSort)); err != nil {
		m.errBar.text = err.Error()
	}
	if err := m.store.SetSetting(chromeSetting, normalizeChrome(m.settings.chrome)); err != nil {
		m.errBar.text = err.Error()
	}
	if err := m.store.SetSetting(leaveSetting, normalizeLeaveMode(m.settings.leaveMode)); err != nil {
		m.errBar.text = err.Error()
	}
	if err := m.store.SetSetting(newSessionAgentSetting, normalizeNewSessionAgent(m.settings.newSessionAgent)); err != nil {
		m.errBar.text = err.Error()
	}
	autoProceed := "off"
	if m.settings.autoProceed {
		autoProceed = "on"
	}
	if err := m.store.SetSetting(autoProceedSetting, autoProceed); err != nil {
		m.errBar.text = err.Error()
	}
	m.autoProceed = m.settings.autoProceed
	m.focusOnEnter = m.settings.enterFocuses
	m.comfortableRows = m.settings.comfortableRows
	m.layout = normalizeLayout(m.settings.layout)
	m.palette = normalizePalette(m.settings.palette)
	m.glyphs = normalizeGlyphs(m.settings.glyphs)
	applyGlyphSet(m.glyphs)
	m.archiveConfirm = normalizeArchiveConfirm(m.settings.archiveConfirm)
	m.listSort = normalizeListSort(m.settings.listSort)
	m.chrome = normalizeChrome(m.settings.chrome)
	m.leaveMode = normalizeLeaveMode(m.settings.leaveMode)
	m.newSessionAgent = normalizeNewSessionAgent(m.settings.newSessionAgent)
}

func (m *Model) openCLIPicker() {
	names := sortedToolNames(m.cfg)
	hidden := make(map[string]bool)
	for name, on := range m.hiddenTools() {
		if on {
			if _, ok := m.cfg.Tools[name]; ok {
				hidden[name] = true
			}
		}
	}
	m.settings.cliPicker = true
	m.settings.cliNames = names
	m.settings.cliHidden = hidden
	m.settings.cliCursor = 0
}

func (m *Model) handleCLIPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	count := len(m.settings.cliNames)
	if count < 1 {
		count = 1
	}
	switch msg.String() {
	case "up", "k":
		m.settings.cliCursor = (m.settings.cliCursor + count - 1) % count
	case "down", "j":
		m.settings.cliCursor = (m.settings.cliCursor + 1) % count
	case " ", "space":
		if m.settings.cliCursor < len(m.settings.cliNames) {
			m.toggleCLIHidden(m.settings.cliNames[m.settings.cliCursor])
		}
	case "enter":
		if m.settings.cliCursor < len(m.settings.cliNames) {
			m.toggleCLIHidden(m.settings.cliNames[m.settings.cliCursor])
		}
	case "esc":
		if err := m.store.SetSetting(hiddenToolsSetting, formatHiddenTools(m.settings.cliHidden)); err != nil {
			m.errBar.text = err.Error()
		}
		m.settings.cliPicker = false
		// Refresh the quick-spawn tool list so it matches the new filter.
		names, index := m.defaultToolSelection()
		m.settings.toolNames = names
		m.settings.toolIndex = index
	}
	return m, nil
}

// toggleCLIHidden flips visibility for one tool. At least one CLI must stay
// enabled so new sessions still have something to launch.
func (m *Model) toggleCLIHidden(name string) {
	if m.settings.cliHidden == nil {
		m.settings.cliHidden = map[string]bool{}
	}
	if m.settings.cliHidden[name] {
		delete(m.settings.cliHidden, name)
		m.errBar.text = ""
		return
	}
	enabled := 0
	for _, toolName := range m.settings.cliNames {
		if !m.settings.cliHidden[toolName] {
			enabled++
		}
	}
	if enabled <= 1 {
		m.errBar.text = "keep at least one CLI enabled"
		return
	}
	m.settings.cliHidden[name] = true
	m.errBar.text = ""
}

// cycleSetting steps the focused setting by one. The theme applies as it
// is stepped so the picker doubles as a live preview of the palette. A theme
// step pushes the pane background to tmux, which shells out, so it returns a
// command rather than blocking the update path.
func (m *Model) cycleSetting(step int) tea.Cmd {
	switch m.settings.field {
	case settingsFieldTool:
		count := len(m.settings.toolNames)
		if count == 0 {
			return nil
		}
		m.settings.toolIndex = (m.settings.toolIndex + step + count) % count
	case settingsFieldAccountRouting:
		if !m.settings.poolAvailable {
			return nil
		}
		if m.settings.accountRouting == accounts.Smart {
			m.settings.accountRouting = accounts.Own
		} else {
			m.settings.accountRouting = accounts.Smart
		}
	case settingsFieldTheme:
		m.settings.themeIndex = (m.settings.themeIndex + step + len(themes)) % len(themes)
		if err := m.store.SetSetting(m.themeSettingKey(), themes[m.settings.themeIndex].Name); err != nil {
			m.errBar.text = "saving theme: " + err.Error()
			m.settings.themeIndex = themeIndex(current.Name)
			return nil
		}
		applyTheme(themes[m.settings.themeIndex])
		SyncTerminalBackground()
		return m.syncPaneTheme()
	case settingsFieldBackdrop:
		m.settings.backdropSync = !m.settings.backdropSync
		mode := backdropInherit
		if m.settings.backdropSync {
			mode = backdropSync
		}
		// The mode changes what every derived tone is mixed from, so the
		// palette is rebuilt before the terminal and the panes are told
		// where the backdrop now is.
		setBackdropMode(mode)
		SyncTerminalBackground()
		return m.syncPaneTheme()
	case settingsFieldDensity:
		m.settings.comfortableRows = !m.settings.comfortableRows
	case settingsFieldLayout:
		index := 0
		for i, mode := range layoutModes {
			if mode == m.settings.layout {
				index = i
			}
		}
		m.settings.layout = layoutModes[(index+step+len(layoutModes))%len(layoutModes)]
	case settingsFieldPalette:
		index := 0
		for i, mode := range paletteModes {
			if mode == m.settings.palette {
				index = i
			}
		}
		// Applied as it is stepped, the way the theme is: the choice is
		// about how the board looks, and the only useful answer is seeing
		// it.
		m.settings.palette = paletteModes[(index+step+len(paletteModes))%len(paletteModes)]
		setPalette(m.settings.palette)
	case settingsFieldGlyphs:
		index := 0
		for i, mode := range glyphsModes {
			if mode == m.settings.glyphs {
				index = i
			}
		}
		// Applied as it is stepped, the way the theme is: the panel is
		// drawn over the board, so the picker doubles as a look at what
		// the marks will be before the choice is saved.
		m.settings.glyphs = glyphsModes[(index+step+len(glyphsModes))%len(glyphsModes)]
		applyGlyphSet(m.settings.glyphs)
	case settingsFieldArchiveConfirm:
		index := 0
		for i, mode := range archiveConfirmModes {
			if mode == m.settings.archiveConfirm {
				index = i
			}
		}
		m.settings.archiveConfirm = archiveConfirmModes[(index+step+len(archiveConfirmModes))%len(archiveConfirmModes)]
	case settingsFieldListSort:
		index := 0
		for i, mode := range listSortModes {
			if mode == m.settings.listSort {
				index = i
			}
		}
		m.settings.listSort = listSortModes[(index+step+len(listSortModes))%len(listSortModes)]
	case settingsFieldChrome:
		index := 0
		for i, mode := range chromeModes {
			if mode == m.settings.chrome {
				index = i
			}
		}
		m.settings.chrome = chromeModes[(index+step+len(chromeModes))%len(chromeModes)]
	case settingsFieldNewSessionAgent:
		index := 0
		for i, mode := range newSessionAgentModes {
			if mode == m.settings.newSessionAgent {
				index = i
			}
		}
		m.settings.newSessionAgent = newSessionAgentModes[(index+step+len(newSessionAgentModes))%len(newSessionAgentModes)]
	case settingsFieldLeave:
		index := 0
		for i, mode := range leaveModes {
			if mode == m.settings.leaveMode {
				index = i
			}
		}
		m.settings.leaveMode = leaveModes[(index+step+len(leaveModes))%len(leaveModes)]
	case settingsFieldQuickClose:
		m.settings.quickCloseSend = !m.settings.quickCloseSend
	case settingsFieldFocusKey:
		m.settings.enterFocuses = !m.settings.enterFocuses
	case settingsFieldAutoProceed:
		m.settings.autoProceed = !m.settings.autoProceed
	}
	return nil
}
