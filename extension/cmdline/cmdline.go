// Package cmdline parses the subcommands the board's executable answers:
// verbs under a group, operands and flags in any order, and a usage printed
// on -h. The host's own commands and an extension's parse the same way, so
// one reads like the other at the shell.
package cmdline

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"syscall"
)

// AnyNumber is a Parse max that places no upper bound on the operands.
const AnyNumber = -1

// Verb is one command under a group: `program group name ...`.
type Verb struct {
	Name  string
	Usage string
	About string
	Run   func(args []string) error
}

// Dispatch runs the verb args names. Help prints every verb's usage to out
// and returns flag.ErrHelp; no verb, or one the group does not have, is an
// error naming the verbs it does.
func Dispatch(out io.Writer, program, group string, verbs []Verb, args []string) error {
	names := make([]string, 0, len(verbs))
	for _, verb := range verbs {
		names = append(names, verb.Name)
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: %s %s <%s>", program, group, strings.Join(names, "|"))
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(out, UsageLines(program, verbs))
		return flag.ErrHelp
	}
	for _, verb := range verbs {
		if verb.Name == args[0] {
			return verb.Run(args[1:])
		}
	}
	return fmt.Errorf("%s has no %q command; it takes %s", group, args[0], strings.Join(names, ", "))
}

// UsageLines is each verb's usage line with its description indented under it.
func UsageLines(program string, verbs []Verb) string {
	var lines strings.Builder
	for _, verb := range verbs {
		lines.WriteString("  " + program + " " + verb.Usage + "\n")
		lines.WriteString("      " + verb.About + "\n")
	}
	return lines.String()
}

// NewFlagSet is a set for one verb, named by its usage line so -h, an unknown
// flag and a miscounted operand all print the same words through Parse. It
// writes nothing itself; Parse prints what the caller should see.
func NewFlagSet(usage string) *flag.FlagSet {
	set := flag.NewFlagSet(usage, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return set
}

// JSONFlag declares --json, which asks for the raw record instead of a
// sentence.
func JSONFlag(set *flag.FlagSet) *bool {
	return set.Bool("json", false, "print the raw result as JSON instead of a sentence")
}

// Emit prints value as indented JSON when asJSON is set, and the human
// sentence otherwise.
func Emit(out io.Writer, asJSON bool, value any, human string) error {
	if asJSON {
		return WriteJSON(out, value)
	}
	_, err := fmt.Fprintln(out, human)
	return err
}

// WriteJSON prints value as JSON indented by two spaces, with a trailing
// newline.
func WriteJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// Parse reads set's flags wherever they sit in args and returns the
// operands, between min and max of them (max may be AnyNumber). The set's
// name is its usage line, so -h, an unknown flag and a miscounted operand all
// print the same words. -h prints that usage and the flags to out and
// returns flag.ErrHelp.
func Parse(out io.Writer, program string, set *flag.FlagSet, args []string, min, max int) ([]string, error) {
	operands, err := Interspersed(set, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(out, "usage: "+program+" "+set.Name())
		set.SetOutput(out)
		set.PrintDefaults()
		return nil, flag.ErrHelp
	}
	if err != nil {
		return nil, fmt.Errorf("%w; usage: %s %s", err, program, set.Name())
	}
	if len(operands) < min || (max != AnyNumber && len(operands) > max) {
		return nil, fmt.Errorf("usage: %s %s", program, set.Name())
	}
	return operands, nil
}

// Interspersed reads flags wherever they sit, since these commands lead
// with their operands and flag.Parse stops at the first one. Anything the
// set has no flag for is an operand, so a message or a title may start with
// a dash the way agent prose often does; everything after "--" is an operand.
func Interspersed(set *flag.FlagSet, args []string) ([]string, error) {
	var literal []string
	if separator := slices.Index(args, "--"); separator >= 0 {
		literal = args[separator+1:]
		args = args[:separator]
	}
	operands := make([]string, 0, len(args)+len(literal))
	for len(args) > 0 {
		if !namesFlag(set, args[0]) {
			operands = append(operands, args[0])
			args = args[1:]
			continue
		}
		if err := set.Parse(args); err != nil {
			return nil, err
		}
		args = set.Args()
	}
	return append(operands, literal...), nil
}

// -h counts wherever it appears: the flag package answers it itself rather
// than declaring it.
func namesFlag(set *flag.FlagSet, token string) bool {
	if token == "-h" || token == "--help" {
		return true
	}
	name, dashed := strings.CutPrefix(token, "-")
	if !dashed {
		return false
	}
	name = strings.TrimPrefix(name, "-")
	name, _, _ = strings.Cut(name, "=")
	return set.Lookup(name) != nil
}

// StateWriteError names the usual reason a session cannot write the board's
// state directory. Claude Code runs a session's shell commands in a sandbox
// whose writable set does not include the config directory, so a CLI
// subcommand fails with "read-only file system" while the board's MCP
// server, a child of the agent rather than of its shell, writes the same
// file without trouble. The bare error reads as the board refusing, so
// the message has to say which door is open. Any other error, and nil, pass
// through unchanged.
func StateWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EROFS) || errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w (a sandboxed shell cannot write the manager's state directory: "+
			"use the Gate Inbox MCP tool instead, or run this command outside the sandbox)", err)
	}
	return err
}
