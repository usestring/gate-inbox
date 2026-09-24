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
	ui       extension.UIHost
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

// StartBoard records what the board tells it in the data directory: every
// status event, every pass, and its own stop. The first subscriber panics on
// every event, which the board has to survive for the second to be told.
func (n *noop) StartBoard(ctx context.Context, board extension.BoardHost) (func(), error) {
	dir, err := n.config.DataDir()
	if err != nil {
		return nil, err
	}
	record := func(name, line string) {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintln(f, line)
	}
	board.Subscribe(func(extension.StatusEvent) { panic("a subscriber that always fails") })
	board.Subscribe(func(e extension.StatusEvent) {
		name := "unreadable"
		if info, err := board.Get(ctx, e.SessionID); err == nil {
			name = info.Name
		}
		record("events.txt", fmt.Sprintf("%s %s>%s %q %s", e.SessionID, e.From, e.To, e.Kind, name))
	})
	board.OnPass(func(p extension.Pass) {
		for _, s := range p.Sessions {
			record("passes.txt", s.ID+" "+s.Status)
			n.ui.Decorate(s.ID, extension.Badge{Text: "noop:" + s.Status, Tone: extension.ToneAccent})
		}
	})
	record("started.txt", fmt.Sprint(os.Getpid(), " ", board.ConfigDir()))
	return func() { record("stopped.txt", fmt.Sprint(ctx.Err() != nil)) }, nil
}

// UI adds one key to the list, which records the row it was pressed on and
// opens a view of it, and one key on that view's screen. It keeps the host
// so StartBoard can badge every row it is told about.
func (n *noop) UI(host extension.UIHost) (extension.UI, error) {
	n.ui = host
	return extension.UI{Keys: []extension.KeyBinding{{
		Action: "noop_mark",
		Keys:   []string{"Z", "shift+z"},
		Label:  "record the row",
		Run: func(ctx context.Context, press extension.Press) error {
			dir, err := n.config.DataDir()
			if err != nil {
				return err
			}
			line := fmt.Sprintf("%s %q %v\n", press.SessionID, press.Group, ctx.Err() == nil)
			if err := os.WriteFile(filepath.Join(dir, "pressed.txt"), []byte(line), 0o600); err != nil {
				return err
			}
			host.Open("peek", &peekView{session: press.SessionID, dir: dir})
			return nil
		},
	}, {
		Screen: "peek",
		Action: "peek_note",
		Keys:   []string{"n"},
		Label:  "note the key",
	}, {
		Action: "noop_compose",
		Keys:   []string{"U"},
		Label:  "compose a note",
		Run: func(context.Context, extension.Press) error {
			dir, err := n.config.DataDir()
			if err != nil {
				return err
			}
			host.Open("compose", &composeForm{dir: dir})
			return nil
		},
	}, {
		Screen: "compose",
		Action: "compose_clear",
		Keys:   []string{"ctrl+l"},
		Label:  "clear the note",
	}}}, nil
}

// composeForm is a view with one field, which records what it is submitted
// with and closes.
type composeForm struct{ dir string }

func (f *composeForm) Title() string { return "noop compose" }

func (f *composeForm) Render(width, height int) []extension.Line {
	return []extension.Line{{{Text: "write a note"}}}
}

func (f *composeForm) Fields() []extension.Field {
	return []extension.Field{{ID: "note", Label: "note", Value: "from"}}
}

func (f *composeForm) Key(key extension.ViewKey) bool {
	if key.Action != extension.ActionSubmit {
		return false
	}
	line := key.Field + " " + key.Values["note"] + "\n"
	_ = os.WriteFile(filepath.Join(f.dir, "submitted.txt"), []byte(line), 0o600)
	return true
}

// peekView shows the row it was opened on, and records every key it is
// told about.
type peekView struct {
	session, dir string
}

func (v *peekView) Title() string { return "noop peek" }

func (v *peekView) Render(width, height int) []extension.Line {
	return []extension.Line{{
		{Text: "row ", Bold: true},
		{Text: v.session, Tone: extension.ToneAccent},
	}}
}

func (v *peekView) Key(key extension.ViewKey) bool {
	f, err := os.OpenFile(filepath.Join(v.dir, "viewkeys.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintf(f, "%s %s\n", key.Action, key.Key)
		f.Close()
	}
	return false
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
