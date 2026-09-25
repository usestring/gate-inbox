package cmdline_test

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/usestring/gate-inbox/extension/cmdline"
)

func newSet(usage string) (*flag.FlagSet, *bool) {
	set := flag.NewFlagSet(usage, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return set, set.Bool("json", false, "print JSON")
}

func TestParseReadsFlagsAmongOperands(t *testing.T) {
	set, asJSON := newSet("send <id> <message>")
	operands, err := cmdline.Parse(io.Discard, "prog", set, []string{"ab12", "-starts with a dash", "--json", "--", "--json"}, 2, cmdline.AnyNumber)
	if err != nil {
		t.Fatal(err)
	}
	if !*asJSON || !slices.Equal(operands, []string{"ab12", "-starts with a dash", "--json"}) {
		t.Fatalf("json=%v operands=%q", *asJSON, operands)
	}
}

func TestParseCountsOperandsAndAnswersHelp(t *testing.T) {
	set, _ := newSet("show <id>")
	if _, err := cmdline.Parse(io.Discard, "prog", set, nil, 1, 1); err == nil || err.Error() != "usage: prog show <id>" {
		t.Fatalf("too few = %v", err)
	}
	var out bytes.Buffer
	set, _ = newSet("show <id>")
	if _, err := cmdline.Parse(&out, "prog", set, []string{"x", "-h"}, 1, 1); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h = %v", err)
	}
	if !strings.HasPrefix(out.String(), "usage: prog show <id>\n") || !strings.Contains(out.String(), "-json") {
		t.Fatalf("-h printed %q", out.String())
	}
}

func TestDispatchRunsTheNamedVerb(t *testing.T) {
	var ran []string
	verbs := []cmdline.Verb{
		{Name: "arm", Usage: "grp arm <id>", About: "arm it", Run: func(args []string) error { ran = args; return nil }},
		{Name: "show", Usage: "grp show <id>", About: "show it", Run: func([]string) error { return nil }},
	}
	if err := cmdline.Dispatch(io.Discard, "prog", "grp", verbs, []string{"arm", "x"}); err != nil || !slices.Equal(ran, []string{"x"}) {
		t.Fatalf("arm: %v, ran %q", err, ran)
	}
	if err := cmdline.Dispatch(io.Discard, "prog", "grp", verbs, nil); err == nil || err.Error() != "usage: prog grp <arm|show>" {
		t.Fatalf("bare = %v", err)
	}
	if err := cmdline.Dispatch(io.Discard, "prog", "grp", verbs, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "it takes arm, show") {
		t.Fatalf("unknown = %v", err)
	}
	var out bytes.Buffer
	if err := cmdline.Dispatch(&out, "prog", "grp", verbs, []string{"help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help = %v", err)
	}
	if want := "  prog grp arm <id>\n      arm it\n  prog grp show <id>\n      show it\n"; out.String() != want {
		t.Fatalf("help printed %q", out.String())
	}
}

func TestStateWriteErrorNamesTheSandbox(t *testing.T) {
	if cmdline.StateWriteError(nil) != nil {
		t.Fatal("nil became an error")
	}
	other := errors.New("disk full")
	if cmdline.StateWriteError(other) != other {
		t.Fatal("an unrelated error was rewritten")
	}
	for _, cause := range []error{syscall.EROFS, fs.ErrPermission} {
		err := cmdline.StateWriteError(fmt.Errorf("write: %w", cause))
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "MCP tool") {
			t.Fatalf("%v became %v", cause, err)
		}
	}
}

func TestEmitPrintsJSONOrTheSentence(t *testing.T) {
	set := cmdline.NewFlagSet("show <id>")
	asJSON := cmdline.JSONFlag(set)
	if _, err := cmdline.Parse(io.Discard, "prog", set, []string{"x", "--json"}, 1, 1); err != nil || !*asJSON {
		t.Fatalf("--json: %v %v", err, *asJSON)
	}
	var out bytes.Buffer
	if err := cmdline.Emit(&out, true, map[string]int{"n": 1}, "one"); err != nil || out.String() != "{\n  \"n\": 1\n}\n" {
		t.Fatalf("json = %q, %v", out.String(), err)
	}
	out.Reset()
	if err := cmdline.Emit(&out, false, map[string]int{"n": 1}, "one"); err != nil || out.String() != "one\n" {
		t.Fatalf("human = %q, %v", out.String(), err)
	}
}
