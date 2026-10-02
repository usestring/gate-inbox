package priority

import "testing"

func TestPriorityOrdering(t *testing.T) {
	tiers := []Tier{Urgent, High, Medium, Unset, Low}
	for i := 1; i < len(tiers); i++ {
		if tiers[i-1].Rank() >= tiers[i].Rank() {
			t.Fatalf("%q does not rank above %q", tiers[i-1], tiers[i])
		}
	}
	if Tier("URGENT!!").Rank() != Unset.Rank() {
		t.Fatal("unknown tier does not rank with the default")
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
		{"lower", Unset, false},
		{"lowest", Unset, false},
		{"3", Urgent, true},
		{"+2", High, true},
		{"1", Medium, true},
		{"0", Unset, true},
		{"-1", Low, true},
		{"-2", Unset, false},
		{"-3", Unset, false},
		{"-4", Unset, false},
		{"4", Unset, false},
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

// An unset tier must not suppress a session or group declaration.
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

func TestGlyphs(t *testing.T) {
	for tier, want := range map[Tier]string{Unset: "", Medium: "▲", High: "▲▲", Urgent: "▲▲▲", Low: "▼"} {
		if got := tier.Glyph(); got != want {
			t.Fatalf("%q draws %q, want %q", tier, got, want)
		}
	}
}

func TestNegativePrioritySurvivesUnset(t *testing.T) {
	if Better(Low, Unset) != Low || Better(Unset, Low) != Low {
		t.Fatal("unset overrides negative priority")
	}
}
