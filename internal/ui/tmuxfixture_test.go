package ui

import (
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// The reaper itself lives in internal/tmuxtest, because every package here
// that drives tmux leaks the same way and only this one used to sweep. What
// stays behind are the names this package's tests already call, bound to it.

// testSocketPrefix names the sockets this package's tests create. It is
// registered in tmuxtest, which is what puts them in reach of the shared
// stray sweep.
func newTestSocket() string { return tmuxtest.NewSocket("ui") }

func clientsOn(socket string) []tmuxtest.Client { return tmuxtest.ClientsOn(socket) }

func reapSocket(socket string) int { return tmuxtest.ReapSocket(socket) }

func awaitClientsOn(socket string, want int, within time.Duration) int {
	return tmuxtest.AwaitClientsOn(socket, want, within)
}
