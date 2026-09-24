package extension_test

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

func TestScrubRedactsAsTheBoardsLogDoes(t *testing.T) {
	key := "sk-" + strings.Repeat("a1", 12)
	cases := []struct{ in, want string }{
		{"export KEY=" + key + " then run", "export KEY=[redacted] then run"},
		{"git clone https://bot:" + strings.Repeat("p", 12) + "@example.com/r.git", "git clone https://bot:[redacted]@example.com/r.git"},
		{`password = "hunter2hunter2"`, `password = "[redacted]"`},
		{"nothing secret here", "nothing secret here"},
	}
	for _, c := range cases {
		if got := extension.Scrub(c.in); got != c.want {
			t.Errorf("Scrub(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
