// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/store"
)

const jevAutoSuggestSetting = "experimental_jev_auto_suggest"
const promptSuggestionsSetting = "experimental_prompt_suggestions"
const compressedFocusSetting = "experimental_compressed_focus"
const reviewQueueSetting = "experimental_review_queue"
const arrowsStaySetting = "experimental_arrows_stay_in_session"

// experiment is one opt-in feature on the Experimental card: the setting it
// persists under and the settingsState flag the card toggles.
type experiment struct {
	setting     string
	name        string
	description string
	flag        func(*settingsState) *bool
}

var experiments = []experiment{
	{jevAutoSuggestSetting, "JEV Auto Suggest", "Suggest the next reply in an existing session; rank New Session prompts.\nNeeds a TypeSafe key (Settings → JEV, or TYPESAFE_API_KEY); sends bounded text to TypeSafe.",
		func(s *settingsState) *bool { return &s.jevAutoSuggest }},
	{promptSuggestionsSetting, "Prompt suggestions", "Reuse recurring prompts in New Session.\nReads local history; sends matches to TypeSafe only with JEV on.",
		func(s *settingsState) *bool { return &s.promptSuggest }},
	{compressedFocusSetting, "Compressed focus view", "Show the shortened conversation in focus mode.\nPrompt and input mirroring is experimental; F3 returns to the terminal.",
		func(s *settingsState) *bool { return &s.compressedFocus }},
	{jevFinishCheckSetting, "JEV finish check", "On finish, say when a Claude session is done, asking the agent when JEV is unsure,\nand relaunch one that stopped without Chrome with --chrome. Needs a TypeSafe key (Settings → JEV, or TYPESAFE_API_KEY); sends its last turns to TypeSafe.",
		func(s *settingsState) *bool { return &s.jevFinishCheck }},
	{jevCopySetting, "JEV copy", "On a key, copy the command, link or value the agent left for you in its last message.\nNeeds a TypeSafe key (Settings → JEV, or TYPESAFE_API_KEY); sends that message to TypeSafe.",
		func(s *settingsState) *bool { return &s.jevCopyOn }},
	{reviewQueueSetting, "Review queue", "Fetch the pull requests you were asked to review on GitHub onto a Reviews tab (]).\nRuns on the ingest key only; [integrations.reviews] sets what counts as an ask.",
		func(s *settingsState) *bool { return &s.reviewQueue }},
	{arrowsStaySetting, "Arrows stay in sessions", "Send Left and Right to the focused session at the prompt's edges too.\nOff, Left at the prompt's head and Right at its end go back to the list.",
		func(s *settingsState) *bool { return &s.arrowsStay }},
}

func storedExperiment(st *store.Store, setting string) bool {
	value, err := st.Setting(setting)
	return err == nil && value == "on"
}

func storedJevAutoSuggest(st *store.Store) bool { return storedExperiment(st, jevAutoSuggestSetting) }

func storedPromptSuggestions(st *store.Store) bool {
	return storedExperiment(st, promptSuggestionsSetting)
}

func storedCompressedFocus(st *store.Store) bool { return storedExperiment(st, compressedFocusSetting) }

func storedReviewQueue(st *store.Store) bool { return storedExperiment(st, reviewQueueSetting) }

func storedArrowsStay(st *store.Store) bool { return storedExperiment(st, arrowsStaySetting) }

func (m *Model) persistExperiments() {
	for _, feature := range experiments {
		value := "off"
		if *feature.flag(&m.settings) {
			value = "on"
		}
		if err := m.store.SetSetting(feature.setting, value); err != nil {
			m.errBar.text = err.Error()
		}
	}
}

func (m *Model) handleExperimentalKey(msg tea.KeyMsg) {
	count := len(experiments)
	switch msg.String() {
	case "up", "k":
		m.settings.experimentalCursor = (m.settings.experimentalCursor + count - 1) % count
	case "down", "j":
		m.settings.experimentalCursor = (m.settings.experimentalCursor + 1) % count
	case "left", "right", "h", "l", "space", "enter":
		flag := experiments[m.settings.experimentalCursor].flag(&m.settings)
		*flag = !*flag
	case "esc":
		m.settings.experimentalPicker = false
	}
}

func (m *Model) viewExperimentalSettings() string {
	var body strings.Builder
	for i, feature := range experiments {
		cursor, state := "  ", "off"
		if i == m.settings.experimentalCursor {
			cursor = "> "
		}
		if *feature.flag(&m.settings) {
			state = "on"
		}
		fmt.Fprintf(&body, "%s%s  ◂ %s ▸\n", cursor, feature.name, state)
	}
	body.WriteString("\n" + experiments[m.settings.experimentalCursor].description)
	return m.cardFlex("▣ Experimental features", body.String(), [][2]string{{"↑↓", "feature"}, {"←→/↵", "toggle"}, {"esc", "back"}})
}
