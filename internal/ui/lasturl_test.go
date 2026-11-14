package ui

import "testing"

func TestLastURLPicksTheNewestLinkAndTrimsProse(t *testing.T) {
	cases := map[string]string{
		"no links here": "",
		"see https://a.example/x then https://b.example/y.": "https://b.example/y",
		"(open https://example.com/p_(q))":                  "https://example.com/p_(q)",
		"[PR](https://github.com/o/r/pull/1), done":         "https://github.com/o/r/pull/1",
		"`http://localhost:3000/path?a=1&b=2`":              "http://localhost:3000/path?a=1&b=2",
		"old https://x.example\nnew http://y.example/z\n":   "http://y.example/z",
		"bare https:// only":                                "",
		"trailing https://.":                                "",
	}
	for in, want := range cases {
		if got := lastURL(in); got != want {
			t.Errorf("lastURL(%q) = %q, want %q", in, got, want)
		}
	}
}
