// Command helpers is a module of its own that uses the extension helper
// packages the way an out-of-tree extension does: through their public
// paths, with nothing under internal/. It runs the group its argument names and
// prints what each helper answered, one line per call.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/extension/decline"
	"github.com/usestring/gate-inbox/extension/gitroot"
	"github.com/usestring/gate-inbox/extension/mcptool"
	"github.com/usestring/gate-inbox/extension/textfmt"
)

func main() {
	groups := map[string]func() error{
		"mcptool": func() error {
			brief := filepath.Join(os.TempDir(), "helpers-brief.md")
			if err := os.WriteFile(brief, []byte("from a file\n"), 0o600); err != nil {
				return err
			}
			text, err := mcptool.RequiredTextArg("", brief, "prompt", "prompt_file")
			fmt.Printf("text=%q err=%v\n", text, err)
			_, err = mcptool.RequiredTextArg("", "", "prompt", "prompt_file")
			fmt.Printf("neither=%v\n", err)
			hints := mcptool.Annotations(true, false, false)
			fmt.Printf("readonly=%v idempotent=%v\n", hints.ReadOnlyHint, hints.IdempotentHint)
			fmt.Printf("result=%T\n", mcptool.Text("done").Content[0])
			return nil
		},
		"textfmt": func() error {
			fmt.Printf("tail=%q\n", textfmt.Tail("head\nthe end of the turn", 19))
			fmt.Printf("first=%q\n", textfmt.FirstLine("one\ntwo", 100))
			fmt.Printf("strip=%q\n", textfmt.StripControl("a\x1bb\n"))
			fmt.Printf("age=%s\n", textfmt.Age(90*time.Minute))
			fmt.Printf("decline=%v\n", decline.LooksLike("I can't help with that."))
			fmt.Printf("oneline=%q\n", textfmt.OneLine(" a\n b "))
			fmt.Printf("wrap=%q\n", textfmt.Wrap("two words", 4))
			fmt.Printf("fingerprint=%v\n", textfmt.Fingerprint("a  b") == textfmt.Fingerprint("a\nb"))
			fmt.Printf("width=%d cut=%q\n", textfmt.Width("\x1b[1m日本\x1b[0m"), textfmt.TruncateWidth("abcdef", 4, "…"))
			return nil
		},
		"cmdline": func() error {
			var got []string
			verbs := []cmdline.Verb{{Name: "arm", Usage: "grp arm <id>", About: "arm it", Run: func(args []string) error {
				set := cmdline.NewFlagSet("grp arm <id>")
				quiet := set.Bool("quiet", false, "say less")
				asJSON := cmdline.JSONFlag(set)
				operands, err := cmdline.Parse(io.Discard, "prog", set, args, 1, 1)
				got = append(operands, fmt.Sprint(*quiet))
				if err != nil {
					return err
				}
				return cmdline.Emit(os.Stdout, *asJSON, map[string]string{"armed": operands[0]}, "armed")
			}}}
			err := cmdline.Dispatch(io.Discard, "prog", "grp", verbs, []string{"arm", "ab12", "--quiet", "--json"})
			fmt.Printf("dispatch=%q err=%v\n", got, err)
			fmt.Printf("state=%v\n", cmdline.StateWriteError(nil))
			return nil
		},
		"gitroot": func() error {
			root, err := gitroot.Superproject("/r/sub", func(dir string, args ...string) (string, error) {
				if dir == "/r/sub" && args[1] == "--show-superproject-working-tree" {
					return "/r", nil
				}
				if args[1] == "--show-toplevel" {
					return dir, nil
				}
				return "", nil
			})
			fmt.Printf("root=%s err=%v\n", root, err)
			fmt.Printf("within=%v outside=%v\n", gitroot.Within("/r/sub/x", "/r"), gitroot.Within("/rx", "/r"))
			return nil
		},
	}
	run, ok := groups[os.Args[1]]
	if !ok {
		fmt.Fprintln(os.Stderr, "no group", os.Args[1])
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
