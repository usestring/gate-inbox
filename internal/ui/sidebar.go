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

// sideBinding is a binding whose horizontal key names a side of the frame
// rather than a direction through a value: the way from the list to the
// pane, and back.
type sideBinding struct {
	// written is the side of the frame the binding's keys are written for.
	// On the other side they are read through mirrorArrow, so one key file
	// means the same thing whichever side the rail is on.
	written string
	// mirroredText is the key map's description on the other side, where
	// the catalog's names the wrong end of the prompt. Empty when the row
	// reads the same on both sides or is not in the key map.
	mirroredText string
}

// sideBindings are written for the left-hand rail, where the pane is to the
// right: → from the list opens a row's work and then focuses its pane, and
// ← folds. With the rail on the right the pane is to the left, so the list
// takes ← to step in and → to fold, and the arrow still points at the pane.
//
// The focused prompt binding is written for the right-hand rail. On the left,
// its horizontal keys are mirrored; outside finished triage, only the arrow
// toward the rail leaves focus.
//
// Everything else horizontal is not here, on purpose. The divider's ← → move
// it in screen columns, so they already follow what is on screen; Settings,
// the reopen card and the pickers step through values.
var sideBindings = map[keymap.Context]map[keymap.Action]sideBinding{
	keymap.ContextList: {
		keymap.StepIn:  {written: config.SidebarLeft},
		keymap.StepOut: {written: config.SidebarLeft},
	},
	keymap.ContextFocus: {
		keymap.BackAtPrompt: {
			written:      config.SidebarRight,
			mirroredText: "Left at prompt's head: list; finished triage: Left/Right next",
		},
	},
}

// mirrors reports whether a binding's keys are read through the mirror on
// this side of the frame.
func (m *Model) mirrors(ctx keymap.Context, action keymap.Action) bool {
	binding, ok := sideBindings[ctx][action]
	return ok && binding.written != normalizeSidebar(m.sidebar)
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

// sideKey is the key a binding answers on this side of the frame: the key
// as bound, or its mirror for a side binding written for the other side.
// The same mapping turns a key pressed on this side back into the key to
// store, since mirroring is its own inverse.
func (m *Model) sideKey(ctx keymap.Context, action keymap.Action, key string) string {
	if !m.mirrors(ctx, action) {
		return key
	}
	return mirrorArrow(key)
}

// sideAction is m.action with the side bindings read through the mirror. A
// press whose mirror is bound to a side binding written for the other side
// is that binding; a press bound to such a binding directly is not, and
// falls through to whatever it would mean without it.
func (m *Model) sideAction(ctx keymap.Context, msg tea.KeyMsg) (keymap.Action, bool) {
	for _, name := range []string{keyName(msg), msg.String()} {
		if mirrored, ok := m.km().Action(ctx, mirrorArrow(name)); ok && m.mirrors(ctx, mirrored) {
			return mirrored, true
		}
	}
	action, bound := m.action(ctx, msg)
	if bound && m.mirrors(ctx, action) {
		return "", false
	}
	return action, bound
}

// sideHelpText is a key map row's description on this side of the frame.
func (m *Model) sideHelpText(ctx keymap.Context, action keymap.Action, text string) string {
	if m.mirrors(ctx, action) && sideBindings[ctx][action].mirroredText != "" {
		return sideBindings[ctx][action].mirroredText
	}
	return text
}
