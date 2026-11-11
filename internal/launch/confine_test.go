package launch

import (
	"errors"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/keyconfine"
	"github.com/usestring/gate-inbox/internal/parentseal"
)

// A CLI with no way to deny itself the key directory (pi here) launches with
// that directory hidden from its whole process tree.
func TestALaunchWithNoDenialOfItsOwnIsConfined(t *testing.T) {
	t.Cleanup(keyconfine.UseForTests(keyconfine.Bubblewrap, nil))
	manager := hooks.NewManager(t.TempDir())
	keyDir := parentseal.KeyDir(manager.ConfigDir())
	command, _, err := Compose(manager, "pi", config.Tool{Command: "sh"}, "sh --session-id x", "abc", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "'bwrap' '--dev-bind' '/' '/' '--die-with-parent' '--tmpfs' '" + keyDir + "' '--remount-ro' '" + keyDir +
		"' '--' '/bin/sh' '-c' 'sh --session-id x'"
	if command != want {
		t.Fatalf("command:\n got %s\nwant %s", command, want)
	}
}

// An opencode pane's attach verb writes into the key directory, so the pane
// command is left whole for the verb to confine the TUI it execs; where
// nothing can confine, the launch is still refused up front.
func TestAnOpencodeLaunchLeavesConfinementToItsAttachVerb(t *testing.T) {
	t.Cleanup(keyconfine.UseForTests(keyconfine.Bubblewrap, nil))
	configDir := t.TempDir()
	command, err := confine(configDir, "opencode", "gate-inbox opencode-attach -- opencode")
	if err != nil || command != "gate-inbox opencode-attach -- opencode" {
		t.Fatalf("got %q, %v; want the attach command untouched", command, err)
	}
	t.Cleanup(keyconfine.UseForTests("", errors.New("bwrap (bubblewrap) is not installed")))
	if _, err := confine(configDir, "opencode", "gate-inbox opencode-attach -- opencode"); err == nil {
		t.Fatal("an opencode launch that cannot be confined must be refused")
	}
}

// A claude launched with the board's settings denies itself the directory, so
// it is left as it was: its own sandbox is the one that applies.
func TestAClaudeLaunchIsNotWrapped(t *testing.T) {
	t.Cleanup(keyconfine.UseForTests(keyconfine.Bubblewrap, nil))
	manager := hooks.NewManager(t.TempDir())
	tool := config.Tool{Command: "sh", StatusSource: hooks.StatusSourceClaude, MCP: "claude"}
	command, _, err := Compose(manager, "claude", tool, "sh", "abc", "", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(command, "bwrap") {
		t.Fatalf("a claude launch should not be wrapped: %s", command)
	}
}

// Where nothing can confine it, the launch is refused, never started bare.
func TestALaunchThatCannotBeConfinedIsRefused(t *testing.T) {
	t.Cleanup(keyconfine.UseForTests("", errors.New("bwrap (bubblewrap) is not installed")))
	manager := hooks.NewManager(t.TempDir())
	_, _, err := Compose(manager, "pi", config.Tool{Command: "sh"}, "sh", "abc", "", "", false, nil)
	if err == nil {
		t.Fatal("an unconfinable launch must be refused")
	}
	for _, part := range []string{"pi cannot be launched", "key directory", "bubblewrap"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("refusal %q should mention %q", err, part)
		}
	}
}

// A terminal's pane runs its shell inside the confinement and ends with it:
// the launch script drops to an unconfined shell after its command exits,
// and an agent drives the terminals it creates, so a confined sh handed an
// empty command, which exits at once, would give it an unconfined shell. An
// empty command names the user's login shell; the pane execs the whole
// wrapped command.
func TestATerminalExecsItsShellInsideTheConfinement(t *testing.T) {
	t.Cleanup(keyconfine.UseForTests(keyconfine.Bubblewrap, nil))
	t.Setenv("SHELL", "/bin/zsh")
	manager := hooks.NewManager(t.TempDir())
	keyDir := parentseal.KeyDir(manager.ConfigDir())
	wrapped := "'bwrap' '--dev-bind' '/' '/' '--die-with-parent' '--tmpfs' '" + keyDir + "' '--remount-ro' '" + keyDir + "' '--' '/bin/sh' '-c' "
	for command, want := range map[string]string{
		"":      "exec " + wrapped + `''\''/bin/zsh'\'' -l'`,
		"sh -i": "exec " + wrapped + "'sh -i'",
	} {
		got, _, err := Compose(manager, "terminal", config.Tool{Command: command, Shell: true}, command, "abc", "", "", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("command %q:\n got %s\nwant %s", command, got, want)
		}
	}
}
