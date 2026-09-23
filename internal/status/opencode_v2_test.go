package status

import "testing"

// The fixtures are verbatim "tmux capture-pane -p" output from opencode 2.0.3
// (--standalone, 140x40, so the sidebar is drawn) on a private tmux socket,
// with only the scratch path shortened. v2 kept the ┃ composer and the ╹
// cutoff, but the finished-turn row lost its ▣ glyph and gained a tok/s
// segment, and mid-turn only the footer spinner shows.
func TestOpencodeV2FixturesMatch(t *testing.T) {
	engine := defaultEngine(t)
	cases := []struct {
		name, want string
	}{
		{"opencode-v2-working.txt", Working},
		{"opencode-v2-turn-finished.txt", Finished},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := engine.Match("opencode", loadPane(t, c.name)); got != c.want {
				t.Fatalf("Match(opencode, %s) = %q want %q", c.name, got, c.want)
			}
		})
	}
}
