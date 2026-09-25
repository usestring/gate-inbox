package textfmt_test

import (
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
)

// The board's own tests hold the full parity corpus against ansi; these pin
// the shapes the fast paths take and that the width is safe to share.
func TestWidthAgreesWithAnsi(t *testing.T) {
	for _, s := range []string{"", "session-42", "\x1b[38;5;61m├─ \x1b[0m◐ agent", "日本語", "👩‍👩‍👧‍👦", "aéb", "\x1b[?25h"} {
		if got, want := textfmt.Width(s), ansi.StringWidth(s); got != want {
			t.Errorf("Width(%q) = %d, ansi = %d", s, got, want)
		}
	}
	for _, s := range []string{"\x1b[1mbold text\x1b[0m here", "├─ wide 日本語 row"} {
		if got, want := textfmt.TruncateWidth(s, 6, "…"), ansi.Truncate(s, 6, "…"); got != want {
			t.Errorf("TruncateWidth(%q) = %q, ansi = %q", s, got, want)
		}
	}
}

// An extension may measure from any goroutine; run with -race this fails if
// the rune memo is unguarded.
func TestWidthIsSafeAcrossGoroutines(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				textfmt.Width(string(rune(0x2500+g*64+i%64)) + "x")
			}
		}()
	}
	wg.Wait()
}
