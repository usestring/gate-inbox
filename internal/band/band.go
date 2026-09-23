// Package band owns the tag that opens every message the manager types into
// a pane on its own account, so the agent reading it, and the transcript
// parser reading it back, can tell it from the user's own typing.
package band

import "strings"

// Tag opens every message the manager sends on its own account.
const Tag = "[gate-inbox]"

// Has reports whether text opens with the tag.
func Has(text string) bool {
	return strings.HasPrefix(text, Tag)
}
