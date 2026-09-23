package ui

import (
	"testing"
)

// A URL that could close the sequence early would paint its own tail into the
// row, so it goes unlinked instead.
func TestHyperlinkRefusesAUrlThatWouldBreakTheRow(t *testing.T) {
	for _, url := range []string{"", "https://x/\x1b]8;;", "https://x/\a", "https://x/\n"} {
		if got := hyperlink(url, "label"); got != "label" {
			t.Errorf("hyperlink(%q) wrapped a URL it cannot trust: %q", url, got)
		}
	}
}
