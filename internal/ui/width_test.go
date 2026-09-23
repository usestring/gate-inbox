package ui

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// widthCorpus is what a frame measures and then some: bare text, the tree
// guides and status glyphs the rail draws, SGR runs of every shape the theme
// emits, wide and combining characters the fast path must refuse, and the
// boundaries between them.
var widthCorpus = []string{
	"", " ", "a", "session-42", strings.Repeat("x", 200),
	"\x1b[0m", "\x1b[38;2;120;130;140msession-42\x1b[0m",
	"\x1b[38;5;61m\x1b[1m├─ \x1b[0m◐ agent \x1b[2m· claude · 3h\x1b[0m",
	"├─ ", "│  ╰─ ", "◐◆●✕○◌", "━━━╸", "🬹🬂🬨", "▀▄▐█▜▟",
	"日本語のテキスト", "a日b", "\x1b[31m日\x1b[0m本",
	"🙂", "👩‍👩‍👧‍👦", "a👩‍👩‍👧‍👦b", "é", "aéb", "́",
	"🇬🇧🇺🇸", "a‍b",
	"tab\there", "line\nbreak", "cr\r\nlf", "bell\a", "del\x7f",
	"\x1b]8;;https://example.com\x07link\x1b]8;;\x07",
	"\x1b[1;2;3;4;5;6;7mmany\x1b[0m", "\x1b(Bplain", "\x1bMreverse-index",
	"trailing\x1b[", "\x1b[38;2;1;2;3", "\x1b",
	// CSI shapes past the SGR run the theme emits: a private parameter, an
	// intermediate byte before the final, and a sequence whose final byte is
	// not 'm'. Each is a branch of the sequence scanner.
	"\x1b[?25h", "\x1b[4 q", "\x1b[>0c", "\x1b[2K", "\x1b[!p", "\x1b[0$~",
	"…", "a…b", "❯ ", "✉3", "⌕pane", "▍label", "°C", "↓ ↑", " nbsp",
}

func TestCellWidthMatchesStringWidth(t *testing.T) {
	for _, s := range widthCorpus {
		if got, want := cellWidth(s), ansi.StringWidth(s); got != want {
			t.Errorf("cellWidth(%q) = %d, ansi.StringWidth = %d", s, got, want)
		}
	}
}

// TestCellWidthMatchesStringWidthRandom shuffles the corpus into strings no
// hand-written case would reach: the fast path turns on a byte's successor,
// so what matters is every seam between an ASCII run and something else.
func TestCellWidthMatchesStringWidthRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(6); n >= 0; n-- {
			b.WriteString(widthCorpus[rng.Intn(len(widthCorpus))])
		}
		s := b.String()
		if got, want := cellWidth(s), ansi.StringWidth(s); got != want {
			t.Fatalf("cellWidth(%q) = %d, ansi.StringWidth = %d", s, got, want)
		}
	}
}

// TestCellWidthMatchesStringWidthBytes runs the same comparison over strings
// that are not valid UTF-8 and not valid ANSI, which a captured pane can hand
// a column mid-sequence.
func TestCellWidthMatchesStringWidthBytes(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	buf := make([]byte, 24)
	for i := 0; i < 20000; i++ {
		buf = buf[:1+rng.Intn(24)]
		for j := range buf {
			buf[j] = byte(rng.Intn(256))
		}
		s := string(buf)
		if got, want := cellWidth(s), ansi.StringWidth(s); got != want {
			t.Fatalf("cellWidth(%q) = %d, ansi.StringWidth = %d", s, got, want)
		}
	}
}

// truncTails are the two tails the frame truncates with, plus a wide one so a
// tail that is not a single cell is covered too.
var truncTails = []string{"", "…", "▶▶"}

func TestCellTruncateMatchesAnsiTruncate(t *testing.T) {
	for _, s := range widthCorpus {
		for _, tail := range truncTails {
			for length := 0; length <= cellWidth(s)+2; length++ {
				got, want := cellTruncate(s, length, tail), ansi.Truncate(s, length, tail)
				if got != want {
					t.Errorf("cellTruncate(%q, %d, %q) = %q, ansi.Truncate = %q", s, length, tail, got, want)
				}
			}
		}
	}
}

// TestCellTruncateMatchesAnsiTruncateRandom shuffles the corpus the way the
// width test does, so the cut lands inside a colour run, between a guide and
// a name, and on either side of a character the fast path must refuse.
func TestCellTruncateMatchesAnsiTruncateRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(6); n >= 0; n-- {
			b.WriteString(widthCorpus[rng.Intn(len(widthCorpus))])
		}
		s := b.String()
		tail := truncTails[rng.Intn(len(truncTails))]
		length := rng.Intn(cellWidth(s) + 3)
		if got, want := cellTruncate(s, length, tail), ansi.Truncate(s, length, tail); got != want {
			t.Fatalf("cellTruncate(%q, %d, %q) = %q, ansi.Truncate = %q", s, length, tail, got, want)
		}
	}
}

// TestCellTruncateMatchesAnsiTruncateBytes runs it over strings that are
// neither valid UTF-8 nor valid ANSI, which a captured pane can hand a column.
func TestCellTruncateMatchesAnsiTruncateBytes(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	buf := make([]byte, 24)
	for i := 0; i < 20000; i++ {
		buf = buf[:1+rng.Intn(24)]
		for j := range buf {
			buf[j] = byte(rng.Intn(256))
		}
		s := string(buf)
		tail := truncTails[rng.Intn(len(truncTails))]
		length := rng.Intn(cellWidth(s) + 3)
		if got, want := cellTruncate(s, length, tail), ansi.Truncate(s, length, tail); got != want {
			t.Fatalf("cellTruncate(%q, %d, %q) = %q, ansi.Truncate = %q", s, length, tail, got, want)
		}
	}
}

// TestSpacesPastTheSharedRun covers the width no shared run holds. A terminal
// wider than the run is the only caller that reaches the fallback, and it is
// the branch a reader is least likely to exercise by accident.
func TestSpacesPastTheSharedRun(t *testing.T) {
	for _, n := range []int{-1, 0, 1, len(spacesRun) - 1, len(spacesRun), len(spacesRun) + 1, 4000} {
		want := 0
		if n > 0 {
			want = n
		}
		if got := spaces(n); len(got) != want || strings.Trim(got, " ") != "" {
			t.Fatalf("spaces(%d) = %q, want %d spaces", n, got, want)
		}
	}
}
