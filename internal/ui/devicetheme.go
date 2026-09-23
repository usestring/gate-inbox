package ui

import (
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func (m *Model) themeSettingKey() string {
	if m.themeDevice == "" {
		return themeSetting
	}
	return themeSetting + ":" + m.themeDevice
}

func (m *Model) loadDeviceTheme(device string) tea.Cmd {
	if device == "" || device == m.themeDevice {
		return nil
	}
	name, err := m.store.Setting(themeSetting + ":" + device)
	if err == nil && name == "" {
		name, err = m.store.Setting(themeSetting)
	}
	if err != nil {
		m.errBar.text = "reading device theme: " + err.Error()
		return nil
	}
	m.themeDevice = device
	applyTheme(themes[themeIndex(name)])
	if m.mode == modeSettings {
		m.settings.themeIndex = themeIndex(current.Name)
	}
	m.lastFrame = ""
	SyncTerminalBackground()
	return m.syncPaneTheme()
}

func (m *Model) initDeviceTheme() {
	if m.ownPane != "" {
		return
	}
	m.themeDevice = tmux.DeviceIdentity(os.Environ())
	if m.themeDevice == "" {
		return
	}
	name, err := m.store.Setting(m.themeSettingKey())
	if err != nil {
		m.errBar.text = "reading device theme: " + err.Error()
		return
	}
	if name != "" {
		applyTheme(themes[themeIndex(name)])
	}
}
