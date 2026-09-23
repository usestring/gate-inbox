// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/envname"
)

// guiEditors are probed on PATH when nothing is configured, in the order
// preferred.
var guiEditors = []string{"code", "cursor", "windsurf", "zed", "subl", "idea"}

// terminalEditors are the last resort, probed on PATH once nothing the
// operator could have chosen deliberately has answered. They exist so no key
// that opens a path can dead-end: a machine with no GUI editor CLI and no
// $EDITOR is ordinary, and `editor` in config.toml is meant as an override
// rather than the entry fee for a feature whose whole interface is a file.
//
// nvim leads because installing it was a choice; vim, nano and vi ship with
// macOS and most Linux images, so their presence says nothing. None are in
// detachedEditors, so each takes the screen through ExecProcess -- the only
// way a terminal editor works at all.
var terminalEditors = []string{"nvim", "vim", "nano", "vi"}

// detachedEditors open a window of their own and return at once, leaving
// the manager on screen. Everything else takes the terminal over, which is
// the safer way round: an editor that draws in the terminal is simply
// broken when started detached, while a windowed one run through
// ExecProcess returns immediately and costs a repaint. An unknown name
// therefore takes the screen rather than disappearing into the background.
var detachedEditors = map[string]bool{
	"code": true, "code-insiders": true, "cursor": true, "windsurf": true,
	"zed": true, "subl": true, "idea": true,
	// The OS openers hand the path to whichever app is registered for it
	// and exit, so a configured "open -a ..." belongs here too.
	"open": true, "xdg-open": true,
}

// lookPath and startEditor are the seams tests swap to control which
// editors this machine has and to observe the launch instead of running it.
var (
	lookPath    = exec.LookPath
	startEditor = func(cmd *exec.Cmd) error {
		if err := cmd.Start(); err != nil {
			return err
		}
		// The manager runs for days at a time; without this every o would
		// leave the finished editor process behind holding its pipes.
		go func() { _ = cmd.Wait() }()
		return nil
	}
)

type editorDoneMsg struct {
	name       string
	path       string
	err        error
	tookScreen bool
}

func (m *Model) openEditor() (tea.Model, tea.Cmd) {
	if _, ok := m.selectedRow(); !ok {
		return m, nil
	}
	dir, ok := m.rowDir()
	if !ok {
		m.errBar.text = "directory no longer exists: " + dir
		return m, nil
	}
	return m.launchEditor(dir)
}

func (m *Model) launchEditor(path string) (tea.Model, tea.Cmd) {
	line := m.resolveEditor()
	cmd, ok := editorCommand(line, path)
	if !ok {
		m.errBar.text = `no editor found: set editor = "code" in config.toml`
		return m, nil
	}
	m.errBar.text = ""
	if !detachedEditors[editorName(line)] {
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return editorDoneMsg{err: err, tookScreen: true}
		})
	}
	return m, startEditorCmd(cmd, editorName(line), path)
}

// Starting a process is exec, which Update must not do: a slow launch
// would hold the next keystroke.
func startEditorCmd(cmd *exec.Cmd, name, path string) tea.Cmd {
	return func() tea.Msg {
		if err := startEditor(cmd); err != nil {
			return editorDoneMsg{err: err}
		}
		return editorDoneMsg{name: name, path: path}
	}
}

// resolveEditor picks the command that opens a path: the configured
// editor, then a GUI editor this machine has. $VISUAL and $EDITOR come
// after those because they usually name the editor set for git commit
// messages, not the one a project is meant to open in; the terminal editors
// probed last are a fallback rather than anything anyone chose.
func (m *Model) resolveEditor() string {
	candidates := []string{m.cfg.Editor, envname.Get(envname.Editor)}
	for _, line := range candidates {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	for _, name := range guiEditors {
		if _, err := lookPath(name); err == nil {
			return name
		}
	}
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if line := strings.TrimSpace(os.Getenv(key)); line != "" {
			return line
		}
	}
	for _, name := range terminalEditors {
		if _, err := lookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// Editor settings and environment variables are parsed as argv, never shell code.
func editorCommand(line, path string) (*exec.Cmd, bool) {
	argv := splitEditorLine(line)
	if len(argv) == 0 {
		return nil, false
	}
	args := append(append([]string{}, argv[1:]...), path)
	return exec.Command(argv[0], args...), true
}

// splitEditorLine splits an editor line into argv, grouping on single and
// double quotes so an argument can carry spaces ("open -a 'Visual Studio
// Code'"). Quoting is all it borrows from a shell; an unclosed quote runs
// to the end of the line rather than failing, since the line is a setting
// rather than a program.
func splitEditorLine(line string) []string {
	var argv []string
	var current strings.Builder
	quote := rune(0)
	quoted := false
	flush := func() {
		if current.Len() > 0 || quoted {
			argv = append(argv, current.String())
			current.Reset()
			quoted = false
		}
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '\'' || r == '"':
			quote, quoted = r, true
		case unicode.IsSpace(r):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return argv
}

// editorName is the command word an editor line starts with: what decides
// where it draws, and what the status line calls it.
func editorName(line string) string {
	argv := splitEditorLine(line)
	if len(argv) == 0 {
		return ""
	}
	return filepath.Base(argv[0])
}
