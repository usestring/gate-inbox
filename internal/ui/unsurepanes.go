package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
)

// foregroundShells are the commands tmux reports for a pane sitting at a shell.
var foregroundShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
}

// atShell reports whether a pane's foreground process is a shell. Such a pane
// is an idle terminal even when it draws a tool's marker: a starship prompt
// is the same character as Claude Code's, so flagging those would name every
// open terminal. A login shell is reported with a leading dash.
func atShell(command string) bool {
	return foregroundShells[strings.TrimPrefix(command, "-")]
}

// unsureListed is how many panes the status line names before counting the rest.
const unsureListed = 3

// unsureShowFor is how long the line stays up. Like the adopt toast it
// outlives a status message's couple of polls: it names something to go and
// look at, and a line gone in two seconds is missed by anyone looking away.
const unsureShowFor = 30 * time.Second

// unsureRemind is how often the line comes back while panes stay unadopted,
// since a missed sighting is exactly the agent nobody is watching.
const unsureRemind = 10 * time.Minute

// noteUnsure flags panes that look like an agent but were not adopted.
//
// An agent the scan cannot place is otherwise invisible: it is not on the
// board, and nothing says it was passed over, so the operator only learns it
// exists by hunting through tmux. The line appears when a pane first shows up
// in that state and again every unsureRemind while any remain, not on every
// scan. A pane that leaves the state (adopted, closed, or back to a plain
// shell) is forgotten so a return flags it again. nil means the scan did not
// see every pane and says nothing about the rest. While the adopt toast is up
// the line waits for a later scan rather than take the toast's keys away.
func (m *Model) noteUnsure(panes []unsurePane, now time.Time) {
	if panes == nil || m.offerLive(now) {
		return
	}
	current := make(map[string]bool, len(panes))
	var fresh []unsurePane
	for _, p := range panes {
		current[p.key] = true
		if !m.unsureFlagged[p.key] {
			fresh = append(fresh, p)
		}
	}
	m.unsureFlagged = current
	if len(fresh) == 0 && (len(panes) == 0 || now.Sub(m.unsureShownAt) < unsureRemind) {
		return
	}
	for _, p := range fresh {
		logging.Warn("pane looks like an agent but was not adopted",
			"session", p.session, "pane", p.pane, "tool", p.tool)
	}
	m.unsureText = unsureLine(panes)
	m.errBar.text = m.unsureText
	m.unsureShownAt = now
}

// unsureLive reports whether the line is still up: nothing has replaced it
// and it has not run out.
func (m *Model) unsureLive(now time.Time) bool {
	return m.unsureText != "" && m.errBar.text == m.unsureText && now.Before(m.unsureShownAt.Add(unsureShowFor))
}

// unsureLine names the unadopted panes, by tmux session and pane id, which is
// enough to find one with `tmux list-panes -a`.
func unsureLine(panes []unsurePane) string {
	names := make([]string, 0, unsureListed)
	for i, p := range panes {
		if i == unsureListed {
			break
		}
		names = append(names, fmt.Sprintf("%s %s (%s)", p.session, p.pane, p.tool))
	}
	rest := ""
	if extra := len(panes) - len(names); extra > 0 {
		rest = fmt.Sprintf(" and %d more", extra)
	}
	return fmt.Sprintf("%d %s %s like an agent but %s not adopted: %s%s",
		len(panes), plural(len(panes), "pane", "panes"), plural(len(panes), "looks", "look"),
		plural(len(panes), "was", "were"), strings.Join(names, ", "), rest)
}
