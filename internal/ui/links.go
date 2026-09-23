// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/clipboard"
)

var (
	// Seams keep link tests from mutating the desktop and clipboard.
	openBrowser    = defaultOpenBrowser
	copyBrowserURL = clipboard.WriteText
)

func defaultOpenBrowser(target string) error {
	return openBrowserWith(runtime.GOOS, os.Getenv("BROWSER"), target, func(cmd *exec.Cmd) error {
		return cmd.Run()
	})
}

func openBrowserWith(goos, browser, target string, run func(*exec.Cmd) error) error {
	commands := browserCommands(goos, browser, target)
	var failures []error
	for _, cmd := range commands {
		err := run(cmd)
		if err == nil {
			return nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", cmd.Args[0], err))
	}
	return errors.Join(failures...)
}

func browserCommands(goos, browser, target string) []*exec.Cmd {
	if goos == "darwin" {
		return []*exec.Cmd{exec.Command("open", target)}
	}

	var commands []*exec.Cmd
	// Keep candidates as argv so shell syntax in a URL remains inert text.
	for _, line := range strings.Split(browser, ":") {
		argv := splitEditorLine(line)
		if len(argv) == 0 {
			continue
		}
		placed := false
		for i := range argv {
			if strings.Contains(argv[i], "%s") {
				argv[i] = strings.ReplaceAll(argv[i], "%s", target)
				placed = true
			}
		}
		if !placed {
			argv = append(argv, target)
		}
		commands = append(commands, exec.Command(argv[0], argv[1:]...))
	}
	return append(commands, exec.Command("xdg-open", target))
}

// hyperlink marks text as an OSC 8 link to url. A terminal that understands
// the sequence opens it on a click; every other one shows the label alone.
//
// It costs no cells -- cellWidth measures the sequence as nothing -- so a
// linked label still lines up with an unlinked one beside it. A URL carrying
// an escape, a bell or a line break would end the sequence early and paint its
// own tail into the row, so it goes unlinked rather than trusted.
func hyperlink(url, text string) string {
	if url == "" || strings.ContainsAny(url, "\x1b\a\n\r") {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

type browserOpenMsg struct {
	target  string
	err     error
	copyErr error
}

func openLink(target string) tea.Cmd {
	return func() tea.Msg {
		err := openBrowser(target)
		if err == nil {
			return browserOpenMsg{target: target}
		}
		return browserOpenMsg{target: target, err: err, copyErr: copyBrowserURL(target)}
	}
}

func (m *Model) handleBrowserOpen(msg browserOpenMsg) {
	if msg.err == nil {
		return
	}
	if msg.copyErr == nil {
		m.errBar.text = fmt.Sprintf("could not open link; URL copied to clipboard: %v", msg.err)
		return
	}
	m.errBar.text = fmt.Sprintf("could not open %s: %v; copying URL: %v", msg.target, msg.err, msg.copyErr)
}
