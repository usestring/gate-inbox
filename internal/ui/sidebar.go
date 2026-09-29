package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/keymap"
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

// sideBindings are the bindings whose horizontal key names a side of the
// frame -- the side the rail is on -- rather than a direction through a
// list or a value. Their keys are written for the default right-hand rail
// and read through mirrorArrow when the rail is on the left, so one binding
// file means the same thing on either side. Each carries the help text the
// left-hand rail shows in place of the catalog's.
//
// Most horizontal keys are not here, on purpose. The list's step in and step
// out (→ ←) are the tree's: → opens a row's work and then focuses the pane,
// and ← folds, whichever side the pane is drawn on -- and with the rail on
// the left, → already points at the pane. The divider's ← → move it in
// screen columns, so they already follow what is on screen. Settings, the
// reopen card and the pickers step through values.
var sideBindings = map[keymap.Context]map[keymap.Action]string{
	keymap.ContextFocus: {
		keymap.BackAtPrompt: "focused, at the prompt's head: back to the list",
	},
}

// sideMirrored reports whether a binding's key follows the rail's side.
func sideMirrored(ctx keymap.Context, action keymap.Action) bool {
	_, ok := sideBindings[ctx][action]
	return ok
}

// mirrorArrow swaps a key for its horizontal mirror image: left and right,
// and vim's h and l, with any modifiers kept. Every other key is its own
// mirror, and mirroring twice gives the key back.
func mirrorArrow(key string) string {
	prefix, base := "", key
	if i := strings.LastIndex(key, "+"); i >= 0 && i < len(key)-1 {
		prefix, base = key[:i+1], key[i+1:]
	}
	switch base {
	case "left":
		base = "right"
	case "right":
		base = "left"
	case "h":
		base = "l"
	case "l":
		base = "h"
	default:
		return key
	}
	return prefix + base
}

// sideKey is the key a side-following binding answers on this side of the
// frame: the key as bound with the rail on the right, its mirror with the
// rail on the left. Any other binding's key is returned as it is. The same
// mapping turns a key pressed on the left back into the key to store, since
// mirroring is its own inverse.
func (m *Model) sideKey(ctx keymap.Context, action keymap.Action, key string) string {
	if m.railOnRight() || !sideMirrored(ctx, action) {
		return key
	}
	return mirrorArrow(key)
}

// sideAction is m.action with the side-following bindings read through the
// mirror. With the rail on the left a key bound to one of them no longer
// answers to it -- its mirror does -- so the pressed key falls through to
// whatever it would mean without the binding.
func (m *Model) sideAction(ctx keymap.Context, msg tea.KeyMsg) (keymap.Action, bool) {
	action, bound := m.action(ctx, msg)
	if m.railOnRight() {
		return action, bound
	}
	if bound && sideMirrored(ctx, action) {
		return "", false
	}
	if bound {
		return action, bound
	}
	for _, name := range []string{keyName(msg), msg.String()} {
		if mirrored, ok := m.km().Action(ctx, mirrorArrow(name)); ok && sideMirrored(ctx, mirrored) {
			return mirrored, true
		}
	}
	return "", false
}

// sideHelpText is a help row's description for this side of the frame.
func (m *Model) sideHelpText(ctx keymap.Context, action keymap.Action, text string) string {
	if left, ok := sideBindings[ctx][action]; ok && m.railOnLeft() {
		return left
	}
	return text
}
