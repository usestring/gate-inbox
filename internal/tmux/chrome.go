package tmux

import (
	"fmt"
	"strings"
)

// ChromeEntry is one session's status bar: what to style and what to call it.
type ChromeEntry struct {
	ID    string
	Label string
}

// chromeBatchMax bounds how many sessions one pass gathers before it measures,
// as captureChainMax bounds a capture chain. It is not what keeps a list
// inside tmux's limit -- splitCommandList does that, by bytes, because a
// session's eleven commands carry its label and a label has no length anyone
// here chose.
const chromeBatchMax = 32

// RefreshChromeBatch restyles and relabels the manager's own sessions in two
// forked tmux processes rather than seven per session.
//
// This is the startup pass. It used to cost `Exists` plus two `show-options`
// plus three `set-option`/`rename-window` runs plus one chained style list --
// seven forks a session, about four hundred on the operator's board, spent
// before the first frame is true. Now it is one command list to read every
// session's prefix keys and one to write every option for every session, on top
// of the single list-sessions that replaced the per-session existence check.
//
// A command list stops at its first failure, so a session that died between
// the listing and the write takes the error and the sessions behind it go in
// the list that follows -- the same recovery a capture chain makes, for the
// same reason.
//
// Adopted panes are skipped rather than failed: their status bar belongs to
// whoever opened them, which is what refuseAdopted says one call at a time.
func (d *Driver) RefreshChromeBatch(entries []ChromeEntry) error {
	live, err := d.managedSessions()
	if err != nil {
		return err
	}
	running := make(map[string]bool, len(live))
	for _, name := range live {
		running[name] = true
	}

	var own []ChromeEntry
	for _, entry := range entries {
		if _, adopted := d.AdoptedTarget(entry.ID); adopted {
			continue
		}
		if !running[sessionName(entry.ID)] {
			continue
		}
		own = append(own, entry)
	}
	if len(own) == 0 {
		return nil
	}

	var failures []error
	for remaining := own; len(remaining) > 0; {
		batch := remaining
		if len(batch) > chromeBatchMax {
			batch = batch[:chromeBatchMax]
		}
		remaining = remaining[len(batch):]
		if err := d.chromeBatch(batch); err != nil {
			// tmux does not say how far a failed list got, so the batch is
			// redone one session at a time -- which is what this pass used to
			// cost for all of them, now paid only where something went wrong.
			failures = append(failures, err)
			for _, entry := range batch {
				_ = d.RefreshChrome(entry.ID)
				_ = d.SetLabel(entry.ID, entry.Label)
			}
		}
	}
	if len(failures) > 0 {
		return failures[0]
	}
	return nil
}

// chromeBatch styles and labels one batch in a single command list.
func (d *Driver) chromeBatch(entries []ChromeEntry) error {
	prefixes, read, err := d.readPrefixes(entries)
	if err != nil {
		return err
	}
	if read < len(entries) {
		return fmt.Errorf("tmux prefix read: %d of %d sessions answered", read, len(entries))
	}

	var commands [][]string
	for i, entry := range entries {
		name := sessionName(entry.ID)
		label := sanitizeFormat(entry.Label)
		commands = append(commands,
			[]string{"set-option", "-t", name, "status", "on"},
			[]string{"set-option", "-t", name, "status-right-length", "100"},
			[]string{"set-option", "-t", name, "status-right", attachStatusRight(prefixes[i][0], prefixes[i][1])},
			[]string{"set-option", "-t", name, "status-style", "bg=colour236,fg=colour249"},
			[]string{"set-option", "-t", name, "window-status-format", ""},
			[]string{"set-option", "-t", name, "window-status-current-format", ""},
			[]string{"set-option", "-t", name, "mouse", "on"},
			[]string{"set-option", "-t", name, "status-left-length", "80"},
			[]string{"set-option", "-t", name, "status-left", " " + label + " "},
			// The window name is the tab, and rename-window also turns tmux's
			// automatic renaming off for it, so the name survives the next
			// command the pane runs.
			[]string{"rename-window", "-t", name, label},
		)
	}
	for _, list := range splitCommandList(commands) {
		out, runErr := d.combined(d.args(commandList(list...)...))
		if runErr != nil {
			return fmt.Errorf("tmux chrome batch: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// readPrefixes reads each session's prefix keys in one command list, which is
// what the status bar's exit hint is built from.
//
// Each value is delimited rather than counted by lines: `show-options -v` on an
// option a session has not set -- which is most of them, the prefix being a
// global -- prints nothing at all, not an empty line, so a line-per-value
// mapping would silently attribute one session's prefix to another.
func (d *Driver) readPrefixes(entries []ChromeEntry) ([][2]string, int, error) {
	sep, err := captureSeparator()
	if err != nil {
		return nil, 0, err
	}
	var commands [][]string
	for _, entry := range entries {
		name := sessionName(entry.ID)
		commands = append(commands,
			[]string{"show-options", "-t", name, "-v", "prefix"},
			[]string{"display-message", "-p", sep},
			[]string{"show-options", "-t", name, "-v", "prefix2"},
			[]string{"display-message", "-p", sep},
		)
	}
	out, err := d.output(d.args(commandList(commands...)...))
	blocks := strings.Split(string(out), sep+"\n")
	if len(blocks) > 0 {
		blocks = blocks[:len(blocks)-1]
	}
	prefixes := make([][2]string, len(entries))
	answered := len(blocks) / 2
	if answered > len(entries) {
		answered = len(entries)
	}
	for i := 0; i < answered; i++ {
		prefixes[i] = [2]string{strings.TrimSpace(blocks[i*2]), strings.TrimSpace(blocks[i*2+1])}
	}
	if err != nil {
		return prefixes, answered, fmt.Errorf("tmux prefix read: %w: %s", err, strings.TrimSpace(string(stderrOf(err))))
	}
	return prefixes, answered, nil
}
