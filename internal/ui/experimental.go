// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/internal/store"
)

const jevAutoSuggestSetting = "experimental_jev_auto_suggest"
const promptSuggestionsSetting = "experimental_prompt_suggestions"

func storedJevAutoSuggest(st *store.Store) bool {
	value, err := st.Setting(jevAutoSuggestSetting)
	return err == nil && value == "on"
}

func storedPromptSuggestions(st *store.Store) bool {
	value, err := st.Setting(promptSuggestionsSetting)
	return err == nil && value == "on"
}

func (m *Model) viewExperimentalSettings() string {
	features := []struct {
		name        string
		enabled     bool
		description string
	}{
		{"JEV Auto Suggest", m.settings.jevAutoSuggest, "Suggest the next reply in an existing session.\nRequires TYPESAFE_API_KEY; sends bounded text to TypeSafe."},
		{"Prompt suggestions", m.settings.promptSuggest, "Reuse recurring prompts in New Session.\nReads local history only; no network requests."},
	}
	var body strings.Builder
	for i, feature := range features {
		cursor, state := "  ", "off"
		if i == m.settings.experimentalCursor {
			cursor = "> "
		}
		if feature.enabled {
			state = "on"
		}
		fmt.Fprintf(&body, "%s%s  ◂ %s ▸\n", cursor, feature.name, state)
	}
	body.WriteString("\n" + features[m.settings.experimentalCursor].description)
	return m.cardFlex("▣ Experimental features", body.String(), [][2]string{{"↑↓", "feature"}, {"←→/↵", "toggle"}, {"esc", "back"}})
}
