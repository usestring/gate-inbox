package ui

import (
	"strings"

	"github.com/usestring/gate-inbox/internal/config"
)

// sidebarSetting stores the settings screen's choice of side for the
// sessions rail. Empty follows [board] sidebar in config.toml; a side
// written here outranks it on this machine.
const sidebarSetting = "sidebar"

// sidebarSides is the setting's cycle order.
var sidebarSides = []string{config.SidebarRight, config.SidebarLeft}

// normalizeSidebar reads anything but left as right, the side the frame
// has always drawn the rail on.
func normalizeSidebar(side string) string {
	if strings.EqualFold(strings.TrimSpace(side), config.SidebarLeft) {
		return config.SidebarLeft
	}
	return config.SidebarRight
}

// storedSidebar resolves the side the rail is drawn on: the settings
// screen's choice when one is stored, else the config file's.
func storedSidebar(st settingReader, configured string) string {
	if st != nil {
		if chosen, err := st.Setting(sidebarSetting); err == nil && strings.TrimSpace(chosen) != "" {
			return normalizeSidebar(chosen)
		}
	}
	return normalizeSidebar(configured)
}

// sidebarOverride is what the settings screen stores for a chosen side:
// nothing when it matches the config file, so that editing the file keeps
// working, and the side itself when it differs.
func sidebarOverride(chosen, configured string) string {
	chosen = normalizeSidebar(chosen)
	if chosen == normalizeSidebar(configured) {
		return ""
	}
	return chosen
}

// railOnLeft reports whether the frame paints the sessions rail down its
// left side, with the session's content to its right.
func (m *Model) railOnLeft() bool { return m.sidebar == config.SidebarLeft }
