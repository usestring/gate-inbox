package snippets

import (
	"testing"
)

// The menu reads bare keys, so there is no chord encoding for a letter to
// survive: every letter validates, including i and m, which no ctrl chord
// could ever carry (ctrl+i IS Tab, ctrl+m IS Enter). This sweep exists so a
// future restriction fails loudly rather than silently stranding a key in a
// file that looks correct.
func TestMenuKeysNeedNoChord(t *testing.T) {
	for r := 'a'; r <= 'z'; r++ {
		letter := string(r)
		set := validate([]Snippet{{Key: letter, Text: "x"}})
		if len(set.Snippets) != 1 || len(set.Problems) != 0 {
			t.Errorf("%q did not bind: %+v, %v", letter, set.Snippets, set.Problems)
		}
		if _, ok := set.Get(letter); !ok {
			t.Errorf("%q does not resolve to its snippet", letter)
		}
	}
}

// The two keys beside the letters bind bare: § in the menu, ± everywhere.
func TestOffAlphabetKeysBindBare(t *testing.T) {
	set := validate([]Snippet{{Key: SectionKey, Text: "x"}, {Key: PlusMinusKey, Text: "y"}})
	if len(set.Snippets) != 2 || len(set.Problems) != 0 {
		t.Fatalf("got %+v, %v; want both", set.Snippets, set.Problems)
	}
	for _, key := range []string{SectionKey, PlusMinusKey} {
		if _, ok := set.Get(key); !ok {
			t.Errorf("%q does not resolve to its snippet", key)
		}
	}
}
