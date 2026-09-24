// A build of the board from outside this module, carrying one extension of
// its own. It imports the public app and extension packages and nothing
// under internal/: the go tool refuses an internal import across modules,
// so this building at all is the proof that the public packages are enough.
// app's boundary test builds it as its own module and drives each of the
// executable's three faces.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/app"
	"github.com/usestring/gate-inbox/extension"
)

// noop does nothing but answer a ping, with its configured greeting and the
// session it was registered for.
type noop struct {
	greeting string
	config   extension.Config
}

type settings struct {
	Greeting string `toml:"greeting"`
}

type pingArgs struct{}

func (*noop) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: "noop", Version: "0.0.1"}
}

func (n *noop) Configure(cfg extension.Config) error {
	s := settings{Greeting: "hello"}
	if err := cfg.Decode(&s); err != nil {
		return err
	}
	n.greeting = s.Greeting
	n.config = cfg
	return nil
}

type noteArgs struct {
	Write string `json:"write,omitempty"`
}

type peekArgs struct {
	ID string `json:"id"`
}

type boardArgs struct {
	ID     string `json:"id"`
	Answer string `json:"answer,omitempty"`
}

func (n *noop) RegisterMCP(r *extension.Registrar, session extension.SessionContext) error {
	err := extension.AddTool(r, &mcp.Tool{
		Name:        "noop_ping",
		Description: "Answer with the configured greeting and this session's id.",
	}, func(context.Context, *mcp.CallToolRequest, pingArgs) (*mcp.CallToolResult, any, error) {
		return text(n.greeting + " from " + session.SessionID), nil, nil
	})
	if err != nil {
		return err
	}
	// noop_note keeps one line in the extension's own data directory,
	// written by one process and read back by another.
	err = extension.AddTool(r, &mcp.Tool{
		Name:        "noop_note",
		Description: "Write a note when given one, then answer with the stored note.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args noteArgs) (*mcp.CallToolResult, any, error) {
		dir, err := n.config.DataDir()
		if err != nil {
			return nil, nil, err
		}
		path := filepath.Join(dir, "note.txt")
		if args.Write != "" {
			if err := os.WriteFile(path+".tmp", []byte(args.Write), 0o600); err != nil {
				return nil, nil, err
			}
			if err := os.Rename(path+".tmp", path); err != nil {
				return nil, nil, err
			}
		}
		note, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		return text(string(note)), nil, nil
	})
	if err != nil {
		return err
	}
	// noop_board reads a session's dialog as the board rather than as this
	// session, through the public parser, and answers it when given words.
	err = extension.AddTool(r, &mcp.Tool{
		Name:        "noop_board",
		Description: "Read a session's dialog as the board, and answer it when given an answer.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args boardArgs) (*mcp.CallToolResult, any, error) {
		board, err := app.NewBoard()
		if err != nil {
			return nil, nil, err
		}
		pane, err := board.ReadPane(ctx, args.ID)
		if err != nil {
			return nil, nil, err
		}
		if pane.Dialog == nil {
			return text("no dialog"), nil, nil
		}
		parsed, ok := extension.InspectDialog(pane.Text)
		out := fmt.Sprintf("%s | %s | %s | parsed:%v", pane.Dialog.Kind, pane.Dialog.Prompt,
			strings.Join(pane.Dialog.Options, ","), ok && parsed.Kind == pane.Dialog.Kind)
		if args.Answer == "" {
			return text(out), nil, nil
		}
		answered, err := board.Answer(ctx, args.ID, args.Answer)
		switch {
		case errors.Is(err, extension.ErrDialogRefused):
			return text(out + " | refused: " + pane.Dialog.Refusal()), nil, nil
		case err != nil:
			return nil, nil, err
		}
		return text(out + " | selected: " + answered.Selected), nil, nil
	})
	if err != nil {
		return err
	}
	// noop_peek reaches the board only through the Host: it lists the
	// sessions this one can see, then reads one of them.
	return extension.AddTool(r, &mcp.Tool{
		Name:        "noop_peek",
		Description: "List the board's sessions, then read one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args peekArgs) (*mcp.CallToolResult, any, error) {
		sessions := session.Host.Sessions()
		list, err := sessions.List(ctx, extension.SessionFilter{})
		if err != nil {
			return nil, nil, err
		}
		ids := make([]string, 0, len(list.Sessions))
		for _, info := range list.Sessions {
			ids = append(ids, info.ID)
		}
		got, err := sessions.Get(ctx, args.ID)
		if err != nil {
			return nil, nil, err
		}
		screen, err := sessions.Read(ctx, args.ID, "")
		if err != nil {
			return nil, nil, err
		}
		return text(fmt.Sprintf("%s | %s %s | %s", strings.Join(ids, ","), got.Name, screen.Mode, screen.Output)), nil, nil
	})
}

// Commands adds noop-echo, which prints its arguments with the configured
// greeting, so a run shows the extension was configured before it, and
// noop-who, which reads the board through the Host it is given. The
// fixture's clash switch also claims a core command's name, which must stop
// the executable from starting.
func (n *noop) Commands() []extension.Command {
	commands := []extension.Command{{
		Group: "Noop fixture",
		Name:  "noop-echo",
		Usage: "noop-echo <words...>",
		About: "print the words after the configured greeting",
		Run: func(_ context.Context, args []string, host extension.Host) error {
			if len(args) == 1 && args[0] == "-h" {
				fmt.Println("usage: gate-inbox noop-echo <words...>")
				return flag.ErrHelp
			}
			if len(args) == 0 {
				return errors.New("noop-echo needs words")
			}
			fmt.Printf("%s: %s (config in %s)\n", n.greeting, strings.Join(args, " "), filepath.Base(host.ConfigDir()))
			return nil
		},
	}, {
		Group: "Noop fixture",
		Name:  "noop-who",
		Usage: "noop-who <id> [message]",
		About: "print who is asking and what the board holds, then send the message",
		Run: func(ctx context.Context, args []string, host extension.Host) error {
			if len(args) == 0 {
				return errors.New("noop-who needs a session id")
			}
			sessions := host.Sessions()
			list, err := sessions.List(ctx, extension.SessionFilter{})
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(list.Sessions))
			for _, info := range list.Sessions {
				ids = append(ids, info.ID)
			}
			got, err := sessions.Get(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("caller %q | %s | %s\n", host.Caller(), strings.Join(ids, ","), got.Name)
			if len(args) > 1 {
				if err := sessions.Send(ctx, args[0], args[1]); err != nil {
					fmt.Printf("send refused: %v\n", err)
				}
			}
			return nil
		},
	}}
	if os.Getenv("NOOP_FIXTURE_CLASH") != "" {
		commands = append(commands, extension.Command{Name: "task", Run: func(context.Context, []string, extension.Host) error { return nil }})
	}
	return commands
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func main() {
	err := app.Run(context.Background(), os.Args[1:], app.Options{
		Extensions: []extension.Extension{&noop{}},
		BuildInfo:  app.BuildInfo{Version: "0.0.0-fixture"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture:", err)
		os.Exit(app.ExitCode(err))
	}
}
