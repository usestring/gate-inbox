package priority

import "testing"

func TestPriorityOrdering(t *testing.T) {
	tiers := []Tier{Urgent, High, Medium, Unset, Low, Lower, Lowest}
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
		{"lower", Lower, true},
		{"lowest", Lowest, true},
		{"3", Urgent, true},
		{"+2", High, true},
		{"1", Medium, true},
		{"0", Unset, true},
		{"-1", Low, true},
		{"-2", Lower, true},
		{"-3", Lowest, true},
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
	for range len(DefaultScale.Tiers()) + 1 {
		tier = DefaultScale.Next(tier)
		seen = append(seen, tier)
	}
	want := []Tier{High, Medium, Low, Unset}
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

func TestScaleTiers(t *testing.T) {
	for _, tc := range []struct {
		scale Scale
		want  []Tier
	}{
		{DefaultScale, []Tier{High, Medium, Low}},
		{Scale{3, 3}, []Tier{Urgent, High, Medium, Low, Lower, Lowest}},
		{Scale{1, 0}, []Tier{Medium}},
		{Scale{9, -4}, []Tier{Urgent, High, Medium}},
		{Scale{0, 2}, []Tier{Medium, Low, Lower}},
	} {
		got := tc.scale.Tiers()
		if len(got) != len(tc.want) {
			t.Fatalf("%+v.Tiers() = %v, want %v", tc.scale, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%+v.Tiers() = %v, want %v", tc.scale, got, tc.want)
			}
		}
	}
}

// A stored tier off the scale reads as the scale's edge, so narrowing the
// scale never hides a session's statement entirely.
func TestScaleClamp(t *testing.T) {
	for _, tc := range []struct {
		scale Scale
		in    Tier
		want  Tier
	}{
		{DefaultScale, Urgent, High},
		{DefaultScale, High, High},
		{DefaultScale, Lowest, Low},
		{DefaultScale, Unset, Unset},
		{Scale{1, 0}, Low, Unset},
		{Scale{3, 3}, Lowest, Lowest},
		{DefaultScale, Tier("bogus"), Unset},
	} {
		if got := tc.scale.Clamp(tc.in); got != tc.want {
			t.Fatalf("%+v.Clamp(%q) = %q, want %q", tc.scale, tc.in, got, tc.want)
		}
	}
}

func TestScaleNextFromOffScaleTier(t *testing.T) {
	if got := DefaultScale.Next(Urgent); got != Medium {
		t.Fatalf("Next(urgent) on +2/-1 = %q, want medium (urgent reads as high)", got)
	}
	if got := (Scale{3, 3}).Next(Low); got != Lower {
		t.Fatalf("Next(low) on +3/-3 = %q, want lower", got)
	}
}

func TestDeferred(t *testing.T) {
	for _, tier := range []Tier{Low, Lower, Lowest} {
		if !tier.Deferred() {
			t.Fatalf("%q is not deferred", tier)
		}
	}
	for _, tier := range []Tier{Urgent, Medium, Unset} {
		if tier.Deferred() {
			t.Fatalf("%q is deferred", tier)
		}
	}
}
