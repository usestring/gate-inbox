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
	"time"

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

// StartBoard records what the board tells it in the data directory: every
// status event, every pass, and its own stop. The first subscriber panics on
// every event, which the board has to survive for the second to be told.
func (n *noop) StartBoard(ctx context.Context, board extension.BoardHost) (func(), error) {
	dir, err := n.config.DataDir()
	if err != nil {
		return nil, err
	}
	record := n.record
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
		}
	})
	record("started.txt", fmt.Sprint(os.Getpid(), " ", board.ConfigDir()))
	switch os.Getenv("NOOP_SCENARIO") {
	case "quiet":
		return func() { record("stopped.txt", fmt.Sprint(ctx.Err() != nil)) }, nil
	case "orphan-hold":
		go n.orphanHold(ctx, board, dir)
		return func() { record("stopped.txt", fmt.Sprint(ctx.Err() != nil)) }, nil
	}
	// A helper of its own, launched from the board under the dead child,
	// with a role and an argument after its prompt.
	go func() {
		helper, err := board.Launch(ctx, extension.LaunchRequest{
			Tool: "envecho", Name: "helper", Prompt: "help", ParentID: "c41d0001",
			Directory: dir, Role: "helper", Args: []string{"--one word"},
		})
		if err != nil {
			record("launched.txt", "error: "+err.Error())
			return
		}
		record("launched.txt", fmt.Sprintf("%s %s %s", helper.ID, helper.Role, helper.ParentID))
		// Message the helper, replace it, then end the replacement, once each
		// pane has started: the board's own acts, with no session to act as.
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(dir, "env-"+helper.ID+".txt")); err == nil {
				break
			}
		}
		sent, err := board.Send(ctx, helper.ID, extension.Message{Text: "carry on", Subject: "note"})
		if err != nil {
			record("sent.txt", "error: "+err.Error())
			return
		}
		record("sent.txt", fmt.Sprintf("%s queued %d", helper.ID, sent.QueuePosition))
		// A held replacement taken back: the trial is its own row under the
		// helper while it runs, and aborting it leaves the helper as it was.
		trial, abandon, err := board.Replace(ctx, helper.ID, extension.LaunchRequest{Name: "trial", Prompt: "try"}, extension.ReplaceOptions{Hold: true})
		if err != nil {
			record("aborted.txt", "error: "+err.Error())
			return
		}
		// Killed during the hold, the trial can no longer be committed:
		// the helper is not given up for a session that is not running.
		waitFor(filepath.Join(dir, "env-"+trial.ID+".txt"))
		if _, err := board.Kill(ctx, trial.ID); err != nil {
			record("aborted.txt", "error: "+err.Error())
			return
		}
		refused := errors.Is(abandon.Commit(ctx), extension.ErrReplacementNotRunning)
		if err := abandon.Abort(ctx); err != nil {
			record("aborted.txt", "error: "+err.Error())
			return
		}
		_, gone := board.Get(ctx, trial.ID)
		kept, err := board.Get(ctx, helper.ID)
		if err != nil {
			record("aborted.txt", "error: "+err.Error())
			return
		}
		record("aborted.txt", fmt.Sprintf("%s held-under %s | refused %v | gone %v | replaced-by %q | old %s %v", trial.ID, trial.ParentID, refused, gone != nil, kept.ReplacedBy, kept.Status, kept.Running))
		// The test reads the helper's inbox before the next replacement
		// takes it.
		waitFor(filepath.Join(dir, "go-commit"))
		// Start the helper over in its own seat, held until the fresh one
		// is seen to run, then committed: filed where the helper was, and
		// the helper left dead.
		fresh, swap, err := board.Replace(ctx, helper.ID, extension.LaunchRequest{Prompt: "again", Args: []string{"--one word"}}, extension.ReplaceOptions{Hold: true})
		if err != nil {
			record("replaced.txt", "error: "+err.Error())
			return
		}
		waitFor(filepath.Join(dir, "env-"+fresh.ID+".txt"))
		during, err := board.Get(ctx, helper.ID)
		if err != nil {
			record("replaced.txt", "error: "+err.Error())
			return
		}
		record("held.txt", fmt.Sprintf("%s held-under %s | old running %v", fresh.ID, fresh.ParentID, during.Running))
		if err := swap.Commit(ctx); err != nil {
			record("replaced.txt", "error: "+err.Error())
			return
		}
		record("settled.txt", fmt.Sprintf("commit again %v | abort after %v", swap.Commit(ctx), errors.Is(swap.Abort(ctx), extension.ErrReplaceSettled)))
		if fresh, err = board.Get(ctx, fresh.ID); err != nil {
			record("replaced.txt", "error: "+err.Error())
			return
		}
		retired, err := board.Get(ctx, helper.ID)
		if err != nil {
			record("replaced.txt", "error: "+err.Error())
			return
		}
		record("replaced.txt", fmt.Sprintf("%s %s %s %s %s replaced-by %s", fresh.ID, fresh.Name, fresh.Role, fresh.ParentID, retired.Status, retired.ReplacedBy))
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(dir, "env-"+fresh.ID+".txt")); err == nil {
				break
			}
		}
		killed, err := board.Kill(ctx, fresh.ID)
		if err != nil {
			record("killed.txt", "error: "+err.Error())
			return
		}
		record("killed.txt", fmt.Sprintf("%s %s %v", killed.ID, killed.Status, killed.Running))
		// A hold the extension never settles: the board aborts it when it
		// stops the extension.
		pending, _, err := board.Replace(ctx, fresh.ID, extension.LaunchRequest{Name: "pending", Prompt: "wait"}, extension.ReplaceOptions{Hold: true})
		if err != nil {
			record("pending.txt", "error: "+err.Error())
			return
		}
		record("pending.txt", fmt.Sprintf("%s held-under %s", pending.ID, pending.ParentID))
	}()
	return func() { record("stopped.txt", fmt.Sprint(ctx.Err() != nil)) }, nil
}

// orphanHold launches a helper and holds a replacement of it that it
// never settles, for a test that kills the board mid-hold.
func (n *noop) orphanHold(ctx context.Context, board extension.BoardHost, dir string) {
	helper, err := board.Launch(ctx, extension.LaunchRequest{
		Tool: "envecho", Name: "helper", Prompt: "help", ParentID: "c41d0001", Directory: dir, Role: "helper",
	})
	if err != nil {
		n.record("orphan.txt", "error: "+err.Error())
		return
	}
	waitFor(filepath.Join(dir, "env-"+helper.ID+".txt"))
	fresh, _, err := board.Replace(ctx, helper.ID, extension.LaunchRequest{Name: "orphan", Prompt: "wait"}, extension.ReplaceOptions{Hold: true})
	if err != nil {
		n.record("orphan.txt", "error: "+err.Error())
		return
	}
	waitFor(filepath.Join(dir, "env-"+fresh.ID+".txt"))
	n.record("orphan.txt", fmt.Sprintf("%s held-under %s", fresh.ID, fresh.ParentID))
}

// waitFor gives path up to ten seconds to appear.
func waitFor(path string) {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
}

// record appends line to a file in the data directory, which is how this
// extension reports to the test driving it from another process.
func (n *noop) record(name, line string) {
	dir, err := n.config.DataDir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// AllowSpawn refuses a session named over-budget, the way a budget on a
// wide spawn would.
func (n *noop) AllowSpawn(_ context.Context, spawn extension.Spawn) error {
	if spawn.Session.Name == "over-budget" {
		return errors.New("the spawn budget is spent")
	}
	return nil
}

func (n *noop) Spawned(_ context.Context, spawn extension.Spawn) {
	n.record("spawned.txt", fmt.Sprintf("%s %s %s", spawn.Session.ID, spawn.By, spawn.Session.Role))
}

// LaunchEnv tells every launch why it happened and where to say so; the
// tool the test launches writes the one into the other.
func (n *noop) LaunchEnv(_ context.Context, launch extension.Launch) (map[string]string, error) {
	dir, err := n.config.DataDir()
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"NOOP_LAUNCH": strings.TrimSpace(string(launch.Reason) + " " + launch.From),
		"NOOP_OUT":    filepath.Join(dir, "env-"+launch.Session.ID+".txt"),
	}, nil
}

func (n *noop) Migrated(_ context.Context, migration extension.Migration) error {
	n.record("migrated.txt", migration.From.ID+">"+migration.To.ID)
	return nil
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
