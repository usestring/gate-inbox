package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestNewSessionOffersTheLastCheckoutWithoutChangingTheDefault(t *testing.T) {
	m := buildModel(t)
	repo := t.TempDir()
	initGitRepo(t, repo)
	checkout := filepath.Join(t.TempDir(), "ticket checkout")
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = repo, tmuxtest.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("worktree", "add", "-b", "ticket", checkout)
	checkout, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	runGit("clone", repo, clone)
	groupAt(t, m, "clone", clone)
	defaultDir := filepath.Join(repo, "project")
	groupAt(t, m, "project", defaultDir)

	// A launch from a subdirectory still makes the checkout root reusable.
	m.openFormFor("project", "ready-tool")
	nested := filepath.Join(checkout, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	m.setPathField(nested)
	pressKey(t, m, enterKey())
	if len(m.sessionRows()) != 1 {
		t.Fatalf("first launch failed: %s", m.errBar.text)
	}
	m.leaveFocusForFixture(t)
	m.selectGroupRow(t, "project")

	pressKey(t, m, instantKey())
	pressKey(t, m, ctrlN())
	m.width, m.height = 52, 24
	view := ansi.Strip(m.viewForm())
	for _, want := range []string{"recent", "ticket ·", "ticket checkout", "F4 use recent"} {
		if !strings.Contains(view, want) {
			t.Fatalf("form is missing %q:\n%s", want, view)
		}
	}
	for _, row := range strings.Split(view, "\n") {
		if textfmt.Width(row) > m.width {
			t.Fatalf("form drew a row wider than the terminal: %q", row)
		}
	}
	if m.form.dir.Value() != defaultDir || m.groupPaths["project"] != defaultDir {
		t.Fatal("offering the recent checkout changed the group default")
	}
	reuse := tea.KeyPressMsg{Code: tea.KeyF4}
	pressKey(t, m, reuse)
	if m.form.dir.Value() != checkout || m.form.dirAuto || m.mode != modeForm || len(m.sessionRows()) != 1 {
		t.Fatalf("F4 should only fill the directory; got %q in mode %v", m.form.dir.Value(), m.mode)
	}

	m.applyCmd(t, m.openFormFor("clone", "ready-tool"))
	if m.hasRecentWorktree() || m.form.dir.Value() != clone {
		t.Fatal("a separate clone inherited the original repository's recent checkout")
	}
	m.applyCmd(t, m.openFormFor("project", "ready-tool"))
	if !m.hasRecentWorktree() {
		t.Fatal("the original repository lost its recent checkout")
	}
	runGit("worktree", "remove", checkout)
	pressKey(t, m, reuse)
	if m.form.dir.Value() != defaultDir || m.hasRecentWorktree() || !strings.Contains(m.errBar.text, "no longer available") {
		t.Fatal("F4 did not refuse a checkout removed while the form was open")
	}
	m.applyCmd(t, m.openFormFor("project", "ready-tool"))
	if m.hasRecentWorktree() {
		t.Fatal("reopening the form still offered the removed checkout")
	}
}
