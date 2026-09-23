package tmux

import (
	"os"
	"strings"
)

// A terminal feature the operator never turned on is, from inside the
// manager, indistinguishable from one the terminal never had. The work card
// and the rail's pull request and ticket rows emit OSC 8 either way, and tmux
// flattens every one of them to its label text unless the attached client is
// declared to understand hyperlinks -- silently, with nothing anywhere saying
// a link was dropped. The operator whose ~/.tmux.conf carries the line clicks
// through to a PR; the one without it sees plain text and reports that links
// do not work here, against a board where they demonstrably do.
//
// `set-clipboard external` is the same shape: the manpage says it ignores an
// application's attempt to set a tmux buffer, so the URL the manager copies
// when no browser can be reached never lands, and the toast still says it
// copied.
//
// The manager cannot settle either of these itself. Both are server- and
// client-scoped settings on a server that belongs to whoever is at the
// keyboard rather than to the manager (see owned), so this reads them and
// names the line that fixes each.

// ConfigFinding is one such setting: what it costs the board, and the
// ~/.tmux.conf line that answers it. Fix is empty where the answer is a
// keystroke rather than a line of config.
type ConfigFinding struct {
	// Key names the finding rather than describing it, so a manager can
	// remember across restarts which findings an operator has already been
	// shown even as the wording changes.
	Key    string
	Detail string
	Fix    string
}

const (
	FindingHyperlinks   = "hyperlinks"
	FindingSetClipboard = "set-clipboard"
	FindingMouseClicks  = "mouse-eats-clicks"
)

// CheckConfig reports what the tmux server the manager's own pane is attached
// to costs the board. That is not necessarily the server the managed sessions
// run on -- GATE_INBOX_TMUX_SOCKET separates them -- and it is the one the
// operator is looking at, so it is the one read here.
//
// A manager started outside tmux has no client whose terminal features could
// be wrong, and gets no findings at all.
func (d *Driver) CheckConfig() []ConfigFinding {
	return d.checkConfig(os.Getenv("TMUX"))
}

func (d *Driver) checkConfig(tmuxEnv string) []ConfigFinding {
	socket, session, ok := clientTarget(tmuxEnv)
	if !ok {
		return nil
	}
	sep, err := captureSeparator()
	if err != nil {
		return nil
	}
	// The client formats are aimed at the manager's own session rather than
	// left to tmux's most-recently-used client: on a server holding several
	// clients an untargeted read answers for whichever one moved last, which
	// is the wrong terminal to diagnose.
	args := append([]string{"-S", socket}, commandList(
		[]string{"display-message", "-p", "-t", session, "#{client_termfeatures}"},
		[]string{"display-message", "-p", sep},
		[]string{"display-message", "-p", "-t", session, "#{client_termname}"},
		[]string{"display-message", "-p", sep},
		[]string{"show-options", "-sv", "set-clipboard"},
		[]string{"display-message", "-p", sep},
		[]string{"show-options", "-gv", "mouse"},
		[]string{"display-message", "-p", sep},
	)...)
	out, err := d.output(args)
	if err != nil {
		// A read that failed says nothing about the operator's config, and a
		// card built from half an answer would name settings it never saw.
		return nil
	}
	blocks := strings.Split(string(out), sep+"\n")
	if len(blocks) < 5 {
		return nil
	}
	for i := range blocks {
		blocks[i] = strings.TrimSpace(blocks[i])
	}
	return configFindings(blocks[0], blocks[1], blocks[2], blocks[3])
}

// clientTarget splits $TMUX -- "<socket path>,<server pid>,<session id>" --
// into the server to ask and the session to ask about. The socket is taken as
// a path rather than a name because a server started with -S lives outside
// tmux's socket directory, where -L cannot reach it.
func clientTarget(tmuxEnv string) (socket, session string, ok bool) {
	fields := strings.Split(tmuxEnv, ",")
	if len(fields) < 3 || fields[0] == "" || fields[2] == "" {
		return "", "", false
	}
	return fields[0], "$" + fields[2], true
}

func configFindings(features, termname, clipboard, mouse string) []ConfigFinding {
	var out []ConfigFinding
	// No features at all means no client answered for that session, not a
	// client missing every feature: reporting the gap here would tell an
	// operator whose terminal is fine to change a line that is already right.
	client := features != ""
	links := client && hasFeature(features, "hyperlinks")

	if client && !links {
		out = append(out, ConfigFinding{
			Key: FindingHyperlinks,
			Detail: "tmux has not been told this terminal understands hyperlinks, so every pull " +
				"request and ticket link on the board is flattened to its label and cannot be " +
				"opened. Neither xterm-256color nor xterm-ghostty declares the capability, so " +
				"tmux withholds it until the line below says otherwise.",
			Fix: "set -as terminal-features '" + termGlob(termname) + ":hyperlinks'",
		})
	}
	if clipboard != "" && clipboard != "on" {
		out = append(out, ConfigFinding{
			Key: FindingSetClipboard,
			Detail: "set-clipboard is " + clipboard + ", which tmux documents as ignoring an " +
				"application's attempt to set the clipboard. When a link cannot be opened the " +
				"board copies its URL instead, and under this setting that copy is dropped " +
				"while the toast still reports it.",
			Fix: "set -s set-clipboard on",
		})
	}
	if links && mouse == "on" {
		out = append(out, ConfigFinding{
			Key: FindingMouseClicks,
			Detail: "mouse mode is on, so a click on a link is delivered to tmux and never " +
				"reaches the terminal's link handler. Shift+click opens one anyway. There is no " +
				"line to add here: the board's own scrolling and pane picking need the mouse, " +
				"so this is a trade rather than a fault.",
		})
	}
	return out
}

// hasFeature matches whole names out of client_termfeatures: "hyperlinks"
// must not be answered by a longer feature that merely contains it.
func hasFeature(features, want string) bool {
	for _, feature := range strings.Split(features, ",") {
		if strings.TrimSpace(feature) == want {
			return true
		}
	}
	return false
}

// termGlob is the terminal-features pattern for a TERM: the family rather
// than the exact name, so the line keeps working when the terminal appends a
// different suffix -- xterm-256color and xterm-ghostty are both xterm*, which
// is how the feature reaches a client that reattached from somewhere else.
func termGlob(termname string) string {
	if termname == "" {
		return "*"
	}
	if family, _, found := strings.Cut(termname, "-"); found && family != "" {
		return family + "*"
	}
	return termname + "*"
}
