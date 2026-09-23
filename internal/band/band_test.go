package band

import "testing"

func TestHas(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"[gate-inbox] From agent \"x\"", true},
		{"[gate-inbox]", true},
		{"fix the [gate-inbox] build", false},
		{" [gate-inbox] leading space", false},
		{"[gate inbox] near miss", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := Has(tc.text); got != tc.want {
			t.Errorf("Has(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
