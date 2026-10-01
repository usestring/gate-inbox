package ui

import "github.com/usestring/gate-inbox/internal/store"

// focusViewSetting is the persisted answer to what a focused session shows:
// the conversation transcript, or the live terminal. The conversation is the
// default: a session entered to read what it said keeps the turns in front of
// you, and F3 hands the pane back to the agent when it is time to type.
const focusViewSetting = "focus_view"

const (
	focusViewTerminal     = "terminal"
	focusViewConversation = "conversation"
)

// focusViewModes is the setting's cycle order.
var focusViewModes = []string{focusViewConversation, focusViewTerminal}

func storedFocusView(st *store.Store) string {
	chosen, err := st.Setting(focusViewSetting)
	if err != nil {
		return focusViewConversation
	}
	return normalizeFocusView(chosen)
}

func normalizeFocusView(chosen string) string {
	for _, mode := range focusViewModes {
		if chosen == mode {
			return mode
		}
	}
	return focusViewConversation
}
