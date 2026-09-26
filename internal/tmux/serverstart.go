package tmux

import (
	"strconv"
	"strings"
	"time"
)

// ServerStarted reports when the manager's tmux server started, and false
// when no server is running or it cannot be read.
//
// It is the evidence that separates a pane the operator closed from one the
// machine lost. A session launched before the server now running started was
// on a server that has since gone -- a reboot, or tmux itself ending -- and
// nobody closed its pane on purpose. One launched after it, whose pane is
// gone while the server stays up, was closed inside that server.
func (d *Driver) ServerStarted() (time.Time, bool) {
	out, err := d.combined(d.args("list-sessions", "-F", "#{start_time}"))
	if err != nil {
		return time.Time{}, false
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	secs, err := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}
