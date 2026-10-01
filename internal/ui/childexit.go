package ui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// exitTailLines is how much of an exited child's pane its spawner is shown.
const exitTailLines = 12

// agentExit reports whether sess's agent has exited in a pane that is still
// up, read from the record the launch script leaves when the agent returns,
// and why. The pane drops to a shell that matches no status rule, so without
// this the row falls to its tool's default status and reads as idle.
func (p *poller) agentExit(sess store.Session) (endClass, bool) {
	if p.hooks == nil || p.shellTools[sess.Tool] {
		return endClass{}, false
	}
	code, at, ok := p.hooks.ReadExit(sess.ID)
	if !ok || at.Before(sess.LaunchTime().Add(-exitClockSlack)) {
		return endClass{}, false
	}
	return classifyExit(code), true
}

// relayChildExit tells sess's spawner that its agent exited, why, and the
// last lines of its pane.
func (p *poller) relayChildExit(sess store.Session, exit endClass, pane string) error {
	return p.enqueueChildRest(sess, status.Errored, childExitMessage(sess, exit, pane))
}

func childExitMessage(sess store.Session, exit endClass, pane string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s (session %s), which you spawned, has exited: %s. Its agent process is gone and "+
		"its pane is left at a shell, so send_session cannot reach it. ", sess.Name, sess.ID, exit.why)
	if tail := paneTail(pane, exitTailLines); tail != "" {
		out.WriteString("The last lines of its pane:\n\n")
		for _, line := range strings.Split(tail, "\n") {
			out.WriteString("  | " + line + "\n")
		}
		out.WriteString("\n")
	}
	fmt.Fprintf(&out, "read_session on %[1]s shows the whole screen. To run it again, kill_session ends the "+
		"leftover shell and revive_session on %[1]s relaunches it; archive_session files it.", sess.ID)
	return out.String()
}

// paneTail is the last n non-blank lines of a pane, escapes stripped and
// credentials scrubbed.
func paneTail(pane string, n int) string {
	var lines []string
	for _, line := range strings.Split(ansi.Strip(pane), "\n") {
		if line = strings.TrimRightFunc(line, unicode.IsSpace); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return logging.ScrubWrapped(strings.Join(lines, "\n"))
}
