package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

type verbs struct {
	id       string
	off      bool
	commands []extension.Command
}

func (v *verbs) Descriptor() extension.Descriptor          { return extension.Descriptor{ID: v.id} }
func (v *verbs) Configure(extension.Config) error          { return nil }
func (v *verbs) Enabled() bool                             { return !v.off }
func (v *verbs) Commands() []extension.Command             { return v.commands }
func noop(context.Context, []string, extension.Host) error { return nil }

func TestExtensionCommandsRefusesEveryBadName(t *testing.T) {
	core := subcommands(context.Background(), "dev", nil)
	_, err := extensionCommands([]extension.Extension{
		&verbs{id: "a", commands: []extension.Command{
			{Name: "Upper", Run: noop},
			{Name: "fine"},
			{Name: "help", Run: noop},
			{Name: "logs", Run: noop},
			{Name: "spawn", Run: noop},
			{Name: "twice", Run: noop},
		}},
		&verbs{id: "b", commands: []extension.Command{{Name: "twice", Run: noop}}},
	}, core)
	if err == nil {
		t.Fatal("no error for a set of bad commands")
	}
	for _, want := range []string{
		`extension "a": command name "Upper" must be lower case`,
		`extension "a": command "fine" has no Run`,
		`extension "a": command "help" is already a command of the core`,
		`extension "a": command "logs" is already a command of the core`,
		`extension "a": command "spawn" is already a command of the core`,
		`extension "b": command "twice" is already a command of extension "a"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not report %q:\n%v", want, err)
		}
	}
}

func TestHelpListsExtensionCommandsByGroupInOrder(t *testing.T) {
	extra, err := extensionCommands([]extension.Extension{
		&verbs{id: "first", commands: []extension.Command{
			{Name: "one", Usage: "one <x>", About: "the first", Run: noop},
			{Group: "Shared", Name: "two", About: "the second", Run: noop},
		}},
		&verbs{id: "second", commands: []extension.Command{{Group: "Shared", Name: "three", About: "the third", Run: noop}}},
	}, subcommands(context.Background(), "dev", nil))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printHelp(&out, extra); err != nil {
		t.Fatal(err)
	}
	want := "\nfirst\n  gate-inbox one <x>\n      the first\n" +
		"\nShared\n  gate-inbox two\n      the second\n  gate-inbox three\n      the third\n" +
		"\nOptions:\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("help does not list the extension commands as %q:\n%s", want, out.String())
	}
}

func TestExtensionCommandOfASwitchedOffExtensionIsRefused(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATE_INBOX_HOME", home)
	ran := false
	ext := &verbs{id: "sleepy", off: true, commands: []extension.Command{{
		Name: "nap",
		Run:  func(context.Context, []string, extension.Host) error { ran = true; return nil },
	}}}
	err := Run(context.Background(), []string{"nap"}, Options{Extensions: []extension.Extension{ext}})
	if err == nil || !strings.Contains(err.Error(), `nap is a command of extension "sleepy", which the config switches off`) || ran {
		t.Fatalf("Run = %v (ran %v), want the switched-off extension's command refused", err, ran)
	}
}
