// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import "github.com/usestring/gate-inbox/internal/store"

const jevAutoSuggestSetting = "experimental_jev_auto_suggest"

func storedJevAutoSuggest(st *store.Store) bool {
	value, err := st.Setting(jevAutoSuggestSetting)
	return err == nil && value == "on"
}
