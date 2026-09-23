// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package systheme reads the terminal background before the TUI owns the tty.
package systheme

import (
	"fmt"
	"sync"
)

// cachedTerminalBg is the OSC 11 round trip, paid once for the process.
// A terminal that never replies costs the whole deadline every time it is asked.
func cachedTerminalBg() (r, g, b int, ok bool) {
	terminalBgOnce.Do(func() {
		terminalBg.r, terminalBg.g, terminalBg.b, terminalBg.ok = queryTerminalBg()
	})
	return terminalBg.r, terminalBg.g, terminalBg.b, terminalBg.ok
}

var (
	terminalBgOnce sync.Once
	terminalBg     struct {
		r, g, b int
		ok      bool
	}
)

// Background returns the terminal background as "#rrggbb". A terminal that
// does not answer, including tmux with no attached client, returns false.
func Background() (string, bool) {
	return background(cachedTerminalBg)
}

func background(queryBg func() (r, g, b int, ok bool)) (string, bool) {
	r, g, b, ok := queryBg()
	if !ok {
		return "", false
	}
	return fmt.Sprintf("#%02x%02x%02x", r, g, b), true
}
