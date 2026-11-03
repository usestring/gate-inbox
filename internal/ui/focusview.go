package ui

import "github.com/usestring/gate-inbox/internal/store"

const focusViewSetting = "focus_view"

const (
	focusViewTerminal     = "terminal"
	focusViewConversation = "conversation"
)

func storedFocusView(st *store.Store) string {
	chosen, err := st.Setting(focusViewSetting)
	if err != nil {
		return focusViewTerminal
	}
	return normalizeFocusView(chosen)
}

func normalizeFocusView(chosen string) string {
	switch chosen {
	case focusViewConversation, focusViewTerminal:
		return chosen
	}
	return focusViewTerminal
}
