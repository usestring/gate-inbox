package ui

import (
	"strings"
	"testing"
)

func TestUnwrapPastedPromptKeepsTheText(t *testing.T) {
	if got := unwrapPastedPrompt(`<pasted_content id="x">second</pasted_content>`); got != "second" {
		t.Fatalf("unwrapPastedPrompt = %q, want %q", got, "second")
	}
	if got := unwrapPastedPrompt("plain"); got != "plain" {
		t.Fatalf("plain prompt changed to %q", got)
	}
}

func TestTruncatePromptCapsAt500Runes(t *testing.T) {
	short := strings.Repeat("a", lastPromptChars)
	if truncatePrompt(short) != short {
		t.Fatal("a prompt at the cap must not be cut")
	}
	long := strings.Repeat("é", lastPromptChars+40)
	got := truncatePrompt(long)
	if n := len([]rune(got)); n != lastPromptChars+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated to %d runes (%q…), want %d ending in …", n, got[:10], lastPromptChars+1)
	}
}
