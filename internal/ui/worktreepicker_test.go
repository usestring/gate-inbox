package ui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestLaunchFormUsesThePickedWorktree(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	checkout := filepath.Join(t.TempDir(), "checkout with spaces")
	add := exec.Command("git", "worktree", "add", "-b", "feature/release", checkout)
	add.Dir, add.Env = repo, tmuxtest.Environ()
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	checkout, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}

	m := buildModel(t)
	m.openForm()
	m.selectTool("ready-tool")
	m.form.name.SetValue("worktree-test")
	m.form.dir.SetValue(repo)
	press := func(code rune) tea.Cmd {
		_, cmd := m.handleFormKey(tea.KeyPressMsg{Code: code})
		return cmd
	}
	if cmd := press(tea.KeyF2); cmd != nil {
		m.applyCmd(t, cmd)
	}
	m.width, m.height = 52, 12
	view := ansi.Strip(m.viewForm())
	for _, want := range []string{"Choose a worktree", "main", "feature/release", "checkout with spaces"} {
		if !strings.Contains(view, want) {
			t.Fatalf("picker does not show %q:\n%s", want, view)
		}
	}
	rows := strings.Split(view, "\n")
	if len(rows) != m.height {
		t.Fatalf("picker drew %d rows in a %d-row terminal", len(rows), m.height)
	}
	for _, row := range rows {
		if textfmt.Width(row) > m.width {
			t.Fatalf("picker drew a row wider than the terminal: %q", row)
		}
	}
	m.width, m.height = 120, 40
	press(tea.KeyDown)
	press(tea.KeyEnter)
	if m.mode != modeForm || m.form.dir.Value() != checkout || m.form.dirAuto {
		t.Fatalf("pick left mode %v, directory %q (default=%v), want %q",
			m.mode, m.form.dir.Value(), m.form.dirAuto, checkout)
	}
	m.applyCmd(t, press(tea.KeyEnter))

	sessions, err := m.store.ListSessions(false)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %+v, err = %v; form error: %s", sessions, err, m.errBar.text)
	}
	paneDir, err := m.tmux.PaneCurrentPath(sessions[0].ID)
	if err != nil || sessions[0].Cwd != checkout || paneDir != checkout {
		t.Fatalf("stored directory %q, pane directory %q (%v), want %q", sessions[0].Cwd, paneDir, err, checkout)
	}
}

func TestWorktreePickerLeavesTheFormIntactOnCancel(t *testing.T) {
	dir := t.TempDir()
	m := buildModel(t)
	m.openForm()
	m.form.dir.SetValue(dir)
	m.form.name.SetValue("keep-this")
	key := tea.KeyPressMsg{Code: tea.KeyF2}

	_, lookup := m.handleFormKey(key)
	m.applyCmd(t, lookup)
	if view := ansi.Strip(m.viewForm()); !strings.Contains(view, "Set the directory to a Git checkout first.") {
		t.Fatalf("missing guidance for a non-repository directory:\n%s", view)
	}
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyEscape})

	_, lookup = m.handleFormKey(key)
	m.handleFormKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.applyCmd(t, lookup)
	if m.mode != modeForm || m.form.worktrees != nil || m.form.dir.Value() != dir || m.form.name.Value() != "keep-this" {
		t.Fatal("cancel or a late result changed the launch form")
	}
}
