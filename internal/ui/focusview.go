package ui

import "github.com/usestring/gate-inbox/internal/store"

// focusViewSetting is the persisted answer to what a focused session shows:
// the live terminal, or the conversation transcript. The terminal is the
// default: focus mode is where keys reach the agent, and the pane is what
// those keys act on. Setting it to the conversation keeps a session's turns
// in front of you until F3 hands the pane back to the agent.
const focusViewSetting = "focus_view"

const (
	focusViewTerminal     = "terminal"
	focusViewConversation = "conversation"
)

// focusViewModes is the setting's cycle order.
var focusViewModes = []string{focusViewTerminal, focusViewConversation}

func storedFocusView(st *store.Store) string {
	chosen, err := st.Setting(focusViewSetting)
	if err != nil {
		return focusViewTerminal
	}
	return normalizeFocusView(chosen)
}

func normalizeFocusView(chosen string) string {
	for _, mode := range focusViewModes {
		if chosen == mode {
			return mode
		}
	}
	return focusViewTerminal
}
