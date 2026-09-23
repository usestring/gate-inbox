package priority

import "testing"

// An untiered session sorts with the mediums rather than ahead of or behind
// every stated tier, which is what keeps adopting tiers from reordering a
// board nobody has triaged.
func TestUnsetRanksWithMedium(t *testing.T) {
	if Unset.Rank() != Medium.Rank() {
		t.Fatalf("unset ranks %d, medium %d", Unset.Rank(), Medium.Rank())
	}
	for i := 1; i < len(Order); i++ {
		if Order[i-1].Rank() >= Order[i].Rank() {
			t.Fatalf("%q does not rank above %q", Order[i-1], Order[i])
		}
	}
}

// A tier that reaches Rank from a hand-edited goal file must not jump the
// queue by being misspelled.
func TestUnknownTierRanksWithTheMiddle(t *testing.T) {
	if got := Tier("URGENT!!").Rank(); got != Medium.Rank() {
		t.Fatalf("an unknown tier ranks %d, want the middle %d", got, Medium.Rank())
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Tier
		ok   bool
	}{
		{"urgent", Urgent, true},
		{"  High ", High, true},
		{"MEDIUM", Medium, true},
		{"low", Low, true},
		{"none", Unset, true},
		{"clear", Unset, true},
		{"unset", Unset, true},
		{"", Unset, true},
		{"critical", Unset, false},
		{"p1", Unset, false},
	} {
		got, ok := Parse(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("Parse(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// The cycle reaches every tier and comes back to unset, because there is no
// second key to clear one with.
func TestNextCyclesThroughUnset(t *testing.T) {
	seen := []Tier{}
	tier := Unset
	for range len(Order) + 1 {
		tier = Next(tier)
		seen = append(seen, tier)
	}
	want := []Tier{Urgent, High, Medium, Low, Unset}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("cycle = %v, want %v", seen, want)
		}
	}
}

// Better keeps the higher tier, and on a tie keeps the stated one -- the
// display has to be able to tell a stated middle from no statement at all.
func TestBetter(t *testing.T) {
	if got := Better(Low, Urgent); got != Urgent {
		t.Fatalf("Better(low, urgent) = %q", got)
	}
	if got := Better(Urgent, Low); got != Urgent {
		t.Fatalf("Better(urgent, low) = %q", got)
	}
	if got := Better(Unset, Medium); got != Medium {
		t.Fatalf("a stated middle lost to no statement: %q", got)
	}
	if got := Better(Medium, Unset); got != Medium {
		t.Fatalf("a stated middle was dropped: %q", got)
	}
}

// Unset draws nothing: no tier is not a tier. Every other glyph is one
// column of geometry, because a terminal gives emoji two.
func TestGlyphs(t *testing.T) {
	if Unset.Glyph() != "" {
		t.Fatalf("unset draws %q", Unset.Glyph())
	}
	seen := map[string]bool{}
	for _, tier := range Order {
		glyph := tier.Glyph()
		if glyph == "" {
			t.Fatalf("%q draws nothing", tier)
		}
		if seen[glyph] {
			t.Fatalf("%q reuses the glyph %q", tier, glyph)
		}
		seen[glyph] = true
		if runes := []rune(glyph); len(runes) != 1 {
			t.Fatalf("%q draws %d runes, want one", tier, len(runes))
		}
	}
}
