package ui

import "github.com/usestring/gate-inbox/internal/store"

// The experimental flag is separate so an older conversation preference
// cannot opt a user into compressed focus without their choosing it.
const focusViewSetting = "focus_view"

const (
	focusViewTerminal     = "terminal"
	focusViewConversation = "conversation"
)

func storedFocusView(st *store.Store) string {
	if !storedCompressedFocus(st) {
		return focusViewTerminal
	}
	chosen, err := st.Setting(focusViewSetting)
	if err != nil {
		return focusViewConversation
	}
	return normalizeFocusView(chosen)
}

func normalizeFocusView(chosen string) string {
	switch chosen {
	case focusViewConversation, focusViewTerminal:
		return chosen
	}
	return focusViewConversation
}
