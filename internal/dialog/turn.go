package dialog

// MaxTurnBytes bounds the worker text carried in an event. The manager reads
// the last turn to see what the worker was doing; a whole turn of tool output
// would bury that in a prompt somebody is paying for.
const MaxTurnBytes = 8 << 10

// Truncate keeps the last max bytes of text, which is the end of a turn --
// what the worker concluded, not how it opened. The cut lands on a rune
// boundary, and on a line boundary when one is close enough to the cut to be
// worth preferring, so the manager is never handed half a word.
func Truncate(text string, max int) string {
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
