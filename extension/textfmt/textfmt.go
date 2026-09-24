// Package textfmt shapes text the board shows or hands on: the end of a
// worker's turn, one line of a message, a body stripped of terminal control
// bytes, and a coarse age. An extension that draws rows or writes events
// beside the board's uses these so the two read alike.
package textfmt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Tail keeps the last max bytes of text, which is the end of a turn --
// what the worker concluded, not how it opened. The cut lands on a rune
// boundary, and on a line boundary when one is close enough to the cut to be
// worth preferring, so the reader is never handed half a word. A max of zero
// or less keeps everything.
func Tail(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	cut := text[len(text)-max:]
	for len(cut) > 0 && !validStart(cut[0]) {
		cut = cut[1:]
	}
	// A line break inside the first tenth of what is left is a cleaner start
	// than a mid-sentence one, and costs at most that tenth.
	if breakAt := indexByte(cut, '\n', len(cut)/10); breakAt >= 0 {
		cut = cut[breakAt+1:]
	}
	return cut
}

// validStart reports whether a byte can begin a UTF-8 rune: anything but a
// continuation byte.
func validStart(b byte) bool { return b&0xC0 != 0x80 }

// indexByte finds b within the first limit bytes of s, or -1.
func indexByte(s string, b byte, limit int) int {
	if limit > len(s) {
		limit = len(s)
	}
	for i := 0; i < limit; i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// FirstLine is the first line of text, trimmed and cut to limit runes with
// "..." marking the cut, for a place that lists messages one to a line.
func FirstLine(text string, limit int) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	if runes := []rune(text); len(runes) > limit {
		text = string(runes[:limit]) + "..."
	}
	return text
}

// StripControl drops the control bytes that would move the cursor or open
// an escape sequence when the text lands in a terminal. Newlines and tabs are
// the text's own shape and stay.
func StripControl(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
}

// Age is a duration as one coarse unit: 35s, 12m, 3h, 2d.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		// Five-second steps: a column of ages that ticks every second is
		// motion the eye chases for no information.
		return fmt.Sprintf("%ds", int(d.Seconds()/5)*5)
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// OneLine collapses every run of whitespace, newlines included, to one space,
// so text someone else chose cannot break the line it is set in.
func OneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// Wrap breaks text to width cells, keeping whole words where it can and
// splitting one that is longer than the width. A width under one is one.
func Wrap(text string, width int) []string {
	return strings.Split(ansi.Wrap(text, max(width, 1), ""), "\n")
}

// Fingerprint is a message's identity for deduplication: its text with the
// whitespace collapsed, hashed, so a retry that only re-wraps the text is
// recognised as the same message. Every sender has to compute it the same
// way for a duplicate to be seen as one.
func Fingerprint(message string) string {
	sum := sha256.Sum256([]byte(OneLine(message)))
	return hex.EncodeToString(sum[:])
}
