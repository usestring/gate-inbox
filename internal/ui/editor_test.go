// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
)

// captureEditor swaps both editor seams: PATH answers only for the names
// given, and the launch is recorded instead of run.
func captureEditor(t *testing.T, installed ...string) *[]string {
	t.Helper()
	var launched []string
	prevLook, prevStart := lookPath, startEditor
	lookPath = func(name string) (string, error) {
		if slices.Contains(installed, name) {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	startEditor = func(cmd *exec.Cmd) error {
		launched = cmd.Args
		return nil
	}
	t.Cleanup(func() { lookPath, startEditor = prevLook, prevStart })
	for _, key := range []string{"GATE_INBOX_EDITOR", "VISUAL", "EDITOR"} {
		t.Setenv(key, "")
	}
	return &launched
}

// A configured editor outranks anything found on PATH, and an argument
// carrying a space stays one argument without a shell to group it.
func TestLaunchEditorPrefersConfiguredCommand(t *testing.T) {
	m := buildModel(t)
	launched := captureEditor(t, "code")
	m.cfg.Editor = `open -a 'Visual Studio Code'`
	dir := t.TempDir()

	_, cmd := m.launchEditor(dir)
	m.applyCmd(t, cmd)

	want := []string{"open", "-a", "Visual Studio Code", dir}
	if !slices.Equal(*launched, want) {
		t.Fatalf("launched %v, want %v", *launched, want)
	}
}

// The line is argv, not a script: a repo that sets EDITOR in an .envrc gets
// no shell to write into, so the operators stay literal text.
func TestEditorLineIsNeverHandedToAShell(t *testing.T) {
	cmd, ok := editorCommand(`code; touch /tmp/pwned`, "/repo")
	if !ok {
		t.Fatal("editorCommand refused a usable line")
	}
	want := []string{"code;", "touch", "/tmp/pwned", "/repo"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("argv = %v, want %v", cmd.Args, want)
	}
}

func TestSplitEditorLineGroupsOnQuotes(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"code", []string{"code"}},
		{"code -n", []string{"code", "-n"}},
		{`open -a "Visual Studio Code"`, []string{"open", "-a", "Visual Studio Code"}},
		{`'/Applications/My App/bin/edit' -w`, []string{"/Applications/My App/bin/edit", "-w"}},
		{"   ", nil},
		{"", nil},
	} {
		if got := splitEditorLine(tc.line); !slices.Equal(got, tc.want) {
			t.Errorf("splitEditorLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// Each boundary of the resolution order, with both neighbours present and
// the higher one expected.
func TestResolveEditorPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured string
		env        map[string]string
		installed  []string
		want       string
	}{
		{"config over environment", "cfg-edit", map[string]string{"GATE_INBOX_EDITOR": "env-edit"}, []string{"code"}, "cfg-edit"},
		{"environment over PATH", "", map[string]string{"GATE_INBOX_EDITOR": "env-edit"}, []string{"code"}, "env-edit"},
		{"first GUI editor on PATH wins", "", nil, []string{"zed", "cursor"}, "cursor"},
		{"PATH over $VISUAL", "", map[string]string{"VISUAL": "vim"}, []string{"code"}, "code"},
		{"$VISUAL over $EDITOR", "", map[string]string{"VISUAL": "vim", "EDITOR": "nano"}, nil, "vim"},
		{"a GUI editor over the terminal fallback", "", nil, []string{"vim", "code"}, "code"},
		{"$EDITOR over the terminal fallback", "", map[string]string{"EDITOR": "ed"}, []string{"vim"}, "ed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{cfg: config.Config{Editor: tc.configured}}
			captureEditor(t, tc.installed...)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			if got := m.resolveEditor(); got != tc.want {
				t.Fatalf("resolveEditor() = %q, want %q", got, tc.want)
			}
		})
	}
}

// $EDITOR is usually the editor set for git, so it only decides when this
// machine has no GUI editor at all - and a terminal editor takes the screen
// rather than being started where it cannot draw.
func TestResolveEditorFallsBackToEnvironment(t *testing.T) {
	m := buildModel(t)
	captureEditor(t)
	t.Setenv("EDITOR", "nvim")

	if got := m.resolveEditor(); got != "nvim" {
		t.Fatalf("resolveEditor() = %q, want nvim", got)
	}
	if detachedEditors[editorName("nvim")] {
		t.Fatal("nvim draws in this terminal and must not start detached")
	}
}

// A machine with no GUI editor CLI and no $EDITOR still has these in
// /usr/bin, so a key that opens a path no longer dead-ends on it.
func TestResolveEditorFallsBackToATerminalEditorOnPATH(t *testing.T) {
	for _, tc := range []struct {
		installed []string
		want      string
	}{
		{[]string{"nvim", "vim", "nano", "vi"}, "nvim"},
		{[]string{"vim", "nano", "vi"}, "vim"},
		{[]string{"nano", "vi"}, "nano"},
		{[]string{"vi"}, "vi"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			m := &Model{}
			captureEditor(t, tc.installed...)
			if got := m.resolveEditor(); got != tc.want {
				t.Fatalf("resolveEditor() = %q, want %q", got, tc.want)
			}
			if detachedEditors[tc.want] {
				t.Fatalf("%s draws in this terminal and must not start detached", tc.want)
			}
		})
	}
}

// An editor nobody listed is handed the screen: wrong for a windowed one
// costs a repaint, wrong for a terminal one loses the editor entirely.
func TestUnknownEditorTakesTheScreen(t *testing.T) {
	m := buildModel(t)
	launched := captureEditor(t)
	m.cfg.Editor = "my-own-edit-wrapper"

	if _, cmd := m.launchEditor(t.TempDir()); cmd == nil {
		t.Fatal("an unknown editor should run through ExecProcess")
	}
	if len(*launched) != 0 {
		t.Fatalf("an unknown editor must not start detached, got %v", *launched)
	}
}

func TestLaunchEditorWithoutAnyEditorExplainsItself(t *testing.T) {
	m := buildModel(t)
	launched := captureEditor(t)

	m.launchEditor(t.TempDir())

	if len(*launched) != 0 {
		t.Fatalf("nothing should launch without an editor, got %v", *launched)
	}
	if !strings.Contains(m.errBar.text, "config.toml") {
		t.Fatalf("status line should point at the setting, got %q", m.errBar.text)
	}
}

// An editor that failed to start leaves its reason on the status line.
func TestEditorFailureLeavesItsReason(t *testing.T) {
	m := buildModel(t)

	updated, cmd := m.Update(editorDoneMsg{err: errors.New("exec: \"code\": file does not exist")})
	*m = *updated.(*Model)
	if cmd != nil {
		t.Fatal("a failed editor should issue no follow-up command")
	}
	if !strings.Contains(m.errBar.text, "does not exist") {
		t.Fatalf("status line should carry the failure, got %q", m.errBar.text)
	}
}
