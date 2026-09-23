package snippets

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// TestChordIsReachable walks all twenty-six letters through the decoder the
// TUI actually reads keys with, and asserts that validate's verdict matches
// what a terminal can really deliver.
//
// This is the test the `unreachable` map exists for. ctrl+alt+<letter> goes on
// the wire as ESC followed by the ctrl byte, and two of those bytes are not
// distinct: ctrl+i IS 0x09, the Tab byte, and ctrl+m IS 0x0d, the Enter byte.
// So ESC 0x09 decodes back as alt+tab and ESC 0x0d as alt+enter, and a snippet
// bound to i or m would sit in the file looking correct and never fire once.
//
// Written as a sweep rather than two cases about i and m so that a decoder
// change in either direction is caught: a letter that stops being reachable
// fails here, and so does one that becomes reachable and is still refused.
func TestChordIsReachable(t *testing.T) {
	for b := byte(1); b <= 26; b++ {
		letter := string(rune('a' + b - 1))
		var decoder uv.EventDecoder
		_, event := decoder.Decode([]byte{0x1b, b})
		press, isKey := event.(uv.KeyPressEvent)
		got := ""
		if isKey {
			got = tea.Key(press).String()
		}

		refusal, refused := unreachable[letter]
		if refused {
			if got == Chord+letter {
				t.Errorf("%s%s is refused as unreachable, but the decoder returns it — drop it from unreachable",
					Chord, letter)
			}
			if got != refusal {
				t.Errorf("%s%s decodes as %q, but unreachable says %q", Chord, letter, got, refusal)
			}
			continue
		}
		if got != Chord+letter {
			t.Errorf("%s%s decodes as %q, so a snippet on %q would never fire — add it to unreachable",
				Chord, letter, got, letter)
		}
	}
}

// Every letter validate accepts must be one the decoder can deliver. This is
// the same claim from the other side: a rule that let a letter through would
// hand the operator a dead key.
func TestValidateAcceptsOnlyReachableLetters(t *testing.T) {
	for r := 'a'; r <= 'z'; r++ {
		letter := string(r)
		set := validate([]Snippet{{Key: letter, Text: "x"}})
		accepted := len(set.Snippets) == 1

		var decoder uv.EventDecoder
		_, event := decoder.Decode([]byte{0x1b, byte(r-'a') + 1})
		press, isKey := event.(uv.KeyPressEvent)
		reachable := isKey && tea.Key(press).String() == Chord+letter

		if accepted != reachable {
			t.Errorf("%s%s: validate accepted=%v, decoder reachable=%v", Chord, letter, accepted, reachable)
		}
	}
}

// TestSectionKeyIsReachable is the same proof for the one key that binds
// outside the chord, and it is the test SectionKey's reasoning exists for.
//
// alt+§ goes on the wire as ESC before the key's own UTF-8, which is a legacy
// encoding tmux forwards untouched; ctrl+§ has no legacy encoding at all,
// because there is no control byte for a non-ASCII key, so a terminal can only
// report it as an extended key. Both assertions matter and neither implies the
// other: that alt+§ arrives, and that it does NOT arrive as a bare §, which is
// the handover key and would be answered instead of the snippet.
func TestSectionKeyIsReachable(t *testing.T) {
	section := []byte("\u00a7")

	alt := append([]byte{0x1b}, section...)
	if got, want := decode(t, alt), SectionChord+SectionKey; got != want {
		t.Errorf("ESC + %s decodes as %q, want %q — the § snippet would never fire", SectionKey, got, want)
	}
	if bare := decode(t, section); bare != SectionKey {
		t.Errorf("a bare %s decodes as %q, want %q", SectionKey, bare, SectionKey)
	}
	if decode(t, alt) == decode(t, section) {
		t.Errorf("%s%s and a bare %s decode alike, so the snippet would steal the handover key",
			SectionChord, SectionKey, SectionKey)
	}
}

// The extended encodings a terminal would have to use for ctrl+§ do decode to
// a distinct key -- the decoder is not what stops it. This pins that down so
// the next reader does not re-derive it from SectionKey's comment and conclude
// the app is at fault: tmux 3.4 drops these before they are ever decoded, and
// this test is the reason the § binding is alt and not ctrl.
func TestCtrlSectionIsDistinctToTheDecoderButNotDeliverable(t *testing.T) {
	for _, seq := range []string{"\x1b[167;5u", "\x1b[27;5;167~"} {
		if got, want := decode(t, []byte(seq)), "ctrl+"+SectionKey; got != want {
			t.Errorf("%q decodes as %q, want %q", seq, got, want)
		}
	}
}

// decode reports what the TUI's own decoder makes of some bytes, or "" when
// they are not a key press at all.
func decode(t *testing.T, seq []byte) string {
	t.Helper()
	var decoder uv.EventDecoder
	_, event := decoder.Decode(seq)
	press, isKey := event.(uv.KeyPressEvent)
	if !isKey {
		return ""
	}
	return tea.Key(press).String()
}
