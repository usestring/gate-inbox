package textfmt

import (
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// Width is the cells s takes in a terminal: ansi.StringWidth with the ASCII case lifted out of the
// grapheme segmenter. StringWidth runs its transition table over the bytes and
// hands every printable one to a fresh grapheme iterator, which at a hundred
// characters a row and fifty rows a frame was a third of the frame's CPU. A
// painted row is nearly all printable ASCII wrapped in SGR sequences, and none
// of it needs segmenting.
//
// The loop below is ansi.stringWidth's own, with two branches added, both
// taken only from the ground state. The first is the printable ASCII byte: the
// table prints 0x20-0x7E, and such a byte is a grapheme cluster by itself
// unless the next byte can join it. Nothing under 0x80 can — every ASCII byte
// is Control, CR, LF or Other in the cluster-break classes, and none of those
// extend a preceding character — so the cell is one and the segmenter has
// nothing to say about it.
//
// The second is the colour sequence, which the table would walk a byte at a
// time and print none of. A painted row is more sequence than text, so it is
// measured in one hop instead. Every other byte takes the original path
// unchanged.
func Width(s string) int {
	if s == "" {
		return 0
	}
	state := parser.GroundState
	width := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if state == parser.GroundState && c >= 0x20 && c < 0x7f &&
			(i+1 == len(s) || s[i+1] < 0x80) {
			width++
			continue
		}
		if state == parser.GroundState && c == 0x1b {
			if size, ok := csiLength(s[i:]); ok {
				i += size - 1
				continue
			}
		}
		next, action := parser.Table.Transition(state, c)
		if action == parser.PrintAction || next == parser.Utf8State {
			if w, size, ok := loneRuneWidth(s[i:]); ok {
				width += w
				i += size - 1
				state = parser.GroundState
				continue
			}
			cluster, w := ansi.FirstGraphemeCluster(s[i:], ansi.GraphemeWidth)
			width += w
			i += len(cluster) - 1
			state = parser.GroundState
			continue
		}
		state = next
	}
	return width
}

// loneRuneCells is what one rune measures standing by itself, learned from the
// segmenter the first time that rune is seen. The rail's tree guides, status
// marks and block glyphs are a couple of dozen runes repeated down every
// column of every frame, and each one was costing a fresh grapheme iterator.
// It is a sync.Map because Width is called from any goroutine, and after the
// first frame nearly every access is a read of a rune already stored.
var (
	loneRuneCells     sync.Map // rune -> int
	loneRuneCellsSize atomic.Int32
)

// loneRuneCellsMax bounds the map. A captured pane can put any script on
// screen, and this is the one memo here whose key comes from outside the
// app's own vocabulary.
const loneRuneCellsMax = 4096

// loneRuneWidth answers for a rune the segmenter cannot join to anything: the
// byte after it is ASCII, and no ASCII byte extends a cluster. What such a
// rune measures is then a fact about the rune alone, so it is asked once.
func loneRuneWidth(s string) (width, size int, ok bool) {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError || size >= len(s) || s[size] >= 0x80 {
		return 0, 0, false
	}
	if w, seen := loneRuneCells.Load(r); seen {
		return w.(int), size, true
	}
	cluster, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
	if len(cluster) != size {
		return 0, 0, false
	}
	if loneRuneCellsSize.Load() < loneRuneCellsMax {
		if _, loaded := loneRuneCells.LoadOrStore(r, w); !loaded {
			loneRuneCellsSize.Add(1)
		}
	}
	return w, size, true
}

// csiLength measures a complete CSI sequence starting at s, which the parser
// table would walk a byte at a time and print none of. A painted row is a
// colour sequence around every few characters, so those bytes outnumber the
// text; here they are one scan and no state.
//
// The shape is the one the table encodes: ESC '[', then parameter bytes
// (0x30-0x3F), then intermediates (0x20-0x2F), then a final byte (0x40-0x7E)
// that dispatches and returns to ground. Anything else -- a truncated
// sequence, a byte out of order -- is handed back to the table unchanged.
func csiLength(s string) (size int, ok bool) {
	if len(s) < 3 || s[1] != '[' {
		return 0, false
	}
	i := 2
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
		i++
	}
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i == len(s) || s[i] < 0x40 || s[i] > 0x7e {
		return 0, false
	}
	return i + 1, true
}

// TruncateWidth cuts s to length cells, tail included: ansi.Truncate with the measurement its callers have already
// paid taken off. ansi.Truncate opens by running StringWidth over the whole
// string to learn whether it has anything to do, which was half its time and
// which a caller painting a row has usually just done via Width.
//
// A row that is printable ASCII wrapped in SGR is then also cut here rather
// than by the segmenter: for that alphabet a printable byte is one cell, and
// every byte of a CSI sequence is copied through whatever the cut is doing,
// because none of them prints. A row carrying anything else -- a wide rune, a
// combining mark, an escape that is not a CSI, a control byte -- is handed
// back to ansi.Truncate whole, so the general answer is still the general
// one. Rail rows mostly take that path, because a tree guide is not ASCII;
// what they keep is the measurement.
func TruncateWidth(s string, length int, tail string) string {
	if Width(s) <= length {
		return s
	}
	if !asciiRow(s) {
		return ansi.Truncate(s, length, tail)
	}
	length -= Width(tail)
	if length < 0 {
		return ""
	}
	var out strings.Builder
	out.Grow(len(s))
	width := 0
	ignoring := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			// asciiRow has already read every sequence in s, so this one is
			// complete and the length is there to be had.
			size, _ := csiLength(s[i:])
			out.WriteString(s[i : i+size])
			i += size
			continue
		}
		if !ignoring && width >= length {
			ignoring = true
			out.WriteString(tail)
		}
		if ignoring {
			i++
			continue
		}
		width++
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// asciiRow reports whether s is printable ASCII and complete CSI sequences and
// nothing else, which is the alphabet TruncateWidth can cut on its own. It is
// asked before anything is built so a row that has to go back to ansi.Truncate
// does not pay for a buffer first.
func asciiRow(s string) bool {
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			size, ok := csiLength(s[i:])
			if !ok {
				return false
			}
			i += size
			continue
		}
		if s[i] < 0x20 || s[i] >= 0x7f {
			return false
		}
		i++
	}
	return true
}
