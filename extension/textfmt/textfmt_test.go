package textfmt_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/usestring/gate-inbox/extension/textfmt"
)

func TestTailKeepsTheEndOnARuneAndLineBoundary(t *testing.T) {
	if got := textfmt.Tail("short", 10); got != "short" {
		t.Fatalf("under the bound = %q", got)
	}
	if got := textfmt.Tail("anything", 0); got != "anything" {
		t.Fatalf("no bound = %q", got)
	}
	// The cut lands inside a three-byte rune; what comes back starts clean.
	text := strings.Repeat("€", 40)
	got := textfmt.Tail(text, 10)
	if !utf8.ValidString(got) || !strings.HasSuffix(text, got) || len(got) > 10 {
		t.Fatalf("mid-rune cut = %q", got)
	}
	// A line break close to the cut is preferred over a mid-sentence start.
	got = textfmt.Tail("opening words\nthe conclusion the worker reached at the end of it", 50)
	if got != "the conclusion the worker reached at the end of it" {
		t.Fatalf("line-boundary cut = %q", got)
	}
}

func TestFirstLineIsOneBoundedLine(t *testing.T) {
	if got := textfmt.FirstLine("  first\nsecond", 100); got != "first" {
		t.Fatalf("multi-line = %q", got)
	}
	if got := textfmt.FirstLine("ééééé", 3); got != "ééé..." {
		t.Fatalf("cut by runes = %q", got)
	}
}

func TestStripControlKeepsTheTextsOwnShape(t *testing.T) {
	if got := textfmt.StripControl("a\x1b[2Jb\tc\nd\x7f\x00"); got != "a[2Jb\tc\nd" {
		t.Fatalf("stripped = %q", got)
	}
}

func TestAgeIsOneCoarseUnit(t *testing.T) {
	for d, want := range map[time.Duration]string{
		37 * time.Second:                "35s",
		12*time.Minute + 59*time.Second: "12m",
		3*time.Hour + 59*time.Minute:    "3h",
		50 * time.Hour:                  "2d",
	} {
		if got := textfmt.Age(d); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestOneLineCollapsesWhitespace(t *testing.T) {
	if got := textfmt.OneLine("  a\n\tb   c \r\n"); got != "a b c" {
		t.Fatalf("OneLine = %q", got)
	}
}

func TestWrapKeepsWordsAndSplitsLongOnes(t *testing.T) {
	got := textfmt.Wrap("keep whole words abcdefghij", 5)
	for _, line := range got {
		if textfmt.Width(line) > 5 {
			t.Fatalf("a line wider than 5: %q", got)
		}
	}
	if got[0] != "keep" || strings.Join(got, "") != "keepwholewordsabcdefghij" {
		t.Fatalf("Wrap = %q", got)
	}
	if got := textfmt.Wrap("ab", 0); len(got) != 2 {
		t.Fatalf("width 0 wraps at one cell: %q", got)
	}
}

func TestFingerprintIgnoresRewrapping(t *testing.T) {
	a := textfmt.Fingerprint("the branch\nmoved to  main")
	if a != textfmt.Fingerprint(" the branch moved to main\n") || len(a) != 64 {
		t.Fatalf("a re-wrapped message fingerprints differently: %s", a)
	}
	if a == textfmt.Fingerprint("the branch moved to dev") {
		t.Fatal("different messages share a fingerprint")
	}
}
