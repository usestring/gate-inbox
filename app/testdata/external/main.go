// A build of the board from outside this module, carrying four extensions of
// its own beside the public artifacts one. It imports the public app and
// extension packages and nothing under internal/: the go tool refuses an internal import across modules,
// so this building at all is the proof that the public packages are enough.
// app's boundary test builds it as its own module and drives each of the
// executable's three faces.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/usestring/gate-inbox/app"
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/extension/artifacts"
)

// noop does nothing but answer a ping, with its configured greeting and the
// session it was registered for.
type noop struct {
	greeting string
	confirm  []string
	config   extension.Config
	ui       extension.UIHost
}

type settings struct {
	Greeting string `toml:"greeting"`
	// Confirm names the sessions the board asks about before opening.
	Confirm []string `toml:"confirm"`
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
	n.confirm = s.Confirm
	n.config = cfg
	return nil
}

type scrubArgs struct {
	Text string `json:"text"`
}

type noteArgs struct {
	Write string `json:"write,omitempty"`
}

type peekArgs struct {
	ID string `json:"id"`
}

type convoArgs struct {
	ID        string `json:"id"`
	Until     string `json:"until,omitempty"`
	AsSession bool   `json:"as_session,omitempty"`
}

// conversations is what Board and a session's SessionService share for
// reading a conversation.
type conversations interface {
	Get(ctx context.Context, id string) (extension.SessionInfo, error)
	Transcript(ctx context.Context, id string) (extension.Transcript, error)
	Handover(ctx context.Context, id string, opts extension.HandoverOptions) (extension.Handover, error)
}

type inboxArgs struct {
	ID      string `json:"id"`
	From    string `json:"from,omitempty"`
	Pending bool   `json:"pending,omitempty"`
}

type endArgs struct {
	ID     string `json:"id"`
	Parent string `json:"parent,omitempty"`
}

type boardArgs struct {
	ID     string `json:"id"`
	Answer string `json:"answer,omitempty"`
}

type captureArgs struct {
	Directory string `json:"directory"`
	Claimed   string `json:"claimed,omitempty"`
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
	// noop_scrub redacts text as the board's log would.
	err = extension.AddTool(r, &mcp.Tool{
		Name:        "noop_scrub",
		Description: "Answer with the text, credentials redacted.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args scrubArgs) (*mcp.CallToolResult, any, error) {
		return text(extension.Scrub(args.Text)), nil, nil
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
	// noop_convo finds a session's conversation, as the board or as this
	// session, and writes the handover copy of it, cut where the quote says
	// when given one.
	err = extension.AddTool(r, &mcp.Tool{
		Name:        "noop_convo",
		Description: "Locate a session's conversation and write its handover copy.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args convoArgs) (*mcp.CallToolResult, any, error) {
		var board conversations = session.Host.Sessions()
		if !args.AsSession {
			var err error
			if board, err = app.NewBoard(); err != nil {
				return nil, nil, err
			}
		}
		info, err := board.Get(ctx, args.ID)
		if err != nil {
			return nil, nil, err
		}
		transcript, err := board.Transcript(ctx, args.ID)
		if err != nil {
			return nil, nil, err
		}
		copied, err := board.Handover(ctx, args.ID, extension.HandoverOptions{Until: args.Until})
		if err != nil {
			return nil, nil, err
		}
		return text(fmt.Sprintf("%s | %s %s | %s cut:%v filtered:%v", info.AgentSessionID,
			transcript.Kind, filepath.Base(transcript.Path), filepath.Base(copied.Path), copied.Cut, copied.Filtered)), nil, nil
	})
	if err != nil {
		return err
	}
	// noop_inbox reads what a session has been sent, as the board.
	err = extension.AddTool(r, &mcp.Tool{
		Name:        "noop_inbox",
		Description: "List what a session has been sent, as the board.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args inboxArgs) (*mcp.CallToolResult, any, error) {
		board, err := app.NewBoard()
		if err != nil {
			return nil, nil, err
		}
		filter := extension.MessageFilter{From: args.From}
		if args.Pending {
			filter.Pending = &args.Pending
		}
		got, err := board.Messages(ctx, args.ID, filter)
		if err != nil {
			return nil, nil, err
		}
		lines := make([]string, 0, len(got))
		for _, msg := range got {
			lines = append(lines, fmt.Sprintf("%s:%s:%v", msg.From, msg.Text, msg.DeliveredAt.IsZero()))
		}
		return text(strings.Join(lines, " | ")), nil, nil
	})
	if err != nil {
		return err
	}
	// noop_tools lists the configured CLIs as the board and as this
	// session's Host, which must agree.
	err = extension.AddTool(r, &mcp.Tool{
		Name:        "noop_tools",
		Description: "List the configured CLIs.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ pingArgs) (*mcp.CallToolResult, any, error) {
		board, err := app.NewBoard()
		if err != nil {
			return nil, nil, err
		}
		fromBoard, err := board.Tools(ctx)
		if err != nil {
			return nil, nil, err
		}
		fromHost, err := session.Host.Tools(ctx)
		if err != nil {
			return nil, nil, err
		}
		render := func(tools []extension.ToolInfo) string {
			lines := make([]string, 0, len(tools))
			for _, tool := range tools {
				lines = append(lines, fmt.Sprintf("%s shell:%v model:%v hooks:%v [%s]", tool.Name,
					tool.Shell, tool.TakesModel, tool.HookStatus, strings.Join(tool.Models, ",")))
			}
			return strings.Join(lines, "\n")
		}
		if a, b := render(fromBoard), render(fromHost); a != b {
			return nil, nil, fmt.Errorf("the board and the host disagree:\n%s\n--\n%s", a, b)
		}
		return text(render(fromBoard)), nil, nil
	})
	if err != nil {
		return err
	}
	// noop_end lists this session's children, terminals included, then
	// ends one of them through the Host.
	err = extension.AddTool(r, &mcp.Tool{
		Name:        "noop_end",
		Description: "List a session's children and terminals, this one's by default, then end one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args endArgs) (*mcp.CallToolResult, any, error) {
		sessions := session.Host.Sessions()
		parent := args.Parent
		if parent == "" {
			parent = "me"
		}
		list, err := sessions.List(ctx, extension.SessionFilter{ParentID: parent, IncludeTerminals: true})
		if err != nil {
			return nil, nil, err
		}
		rows := make([]string, 0, len(list.Sessions))
		for _, info := range list.Sessions {
			rows = append(rows, fmt.Sprintf("%s terminal=%v", info.ID, info.Terminal))
		}
		if err := sessions.Kill(ctx, args.ID); err != nil {
			return text(strings.Join(rows, ",") + " | refused: " + err.Error()), nil, nil
		}
		return text(strings.Join(rows, ",") + " | ended " + args.ID), nil, nil
	})
	if err != nil {
		return err
	}
	// noop_stamps reads when a session was created and archived, as Unix
	// nanoseconds, with 0 for a time the board has none of.
	err = extension.AddTool(r, &mcp.Tool{
		Name:        "noop_stamps",
		Description: "Say when a session was created and archived.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args peekArgs) (*mcp.CallToolResult, any, error) {
		got, err := session.Host.Sessions().Get(ctx, args.ID)
		if err != nil {
			return nil, nil, err
		}
		archived := int64(0)
		if !got.ArchivedAt.IsZero() {
			archived = got.ArchivedAt.UnixNano()
		}
		return text(fmt.Sprintf("%d %d", got.CreatedAt.UnixNano(), archived)), nil, nil
	})
	if err != nil {
		return err
	}
	// noop_capture runs the echo driver's id capture, which the board
	// otherwise only reaches from its poller, so a test can drive it.
	err = extension.AddTool(r, &mcp.Tool{
		Name:        "noop_capture",
		Description: "Capture the echo conversation launched in a directory.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args captureArgs) (*mcp.CallToolResult, any, error) {
		id, err := echoDriver{}.CaptureSession(ctx, extension.CaptureRequest{
			Directory: args.Directory,
			Claimed:   func(id string) bool { return id == args.Claimed },
		})
		if err != nil {
			return nil, nil, err
		}
		return text("captured " + id), nil, nil
	})
	if err != nil {
		return err
	}
	// noop_peek reaches the board only through the Host: it lists the
	// sessions this one can see a page of one at a time, then reads one of
	// them.
	return extension.AddTool(r, &mcp.Tool{
		Name:        "noop_peek",
		Description: "List the board's sessions, then read one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args peekArgs) (*mcp.CallToolResult, any, error) {
		session.Host.Logger().Debug("noop_peek", "id", args.ID)
		sessions := session.Host.Sessions()
		var ids []string
		pages := 0
		for filter := (extension.SessionFilter{Limit: 1}); ; {
			list, err := sessions.List(ctx, filter)
			if err != nil {
				return nil, nil, err
			}
			pages++
			for _, info := range list.Sessions {
				ids = append(ids, info.ID)
			}
			if list.Cursor == "" {
				break
			}
			filter.After = list.Cursor
		}
		got, err := sessions.Get(ctx, args.ID)
		if err != nil {
			return nil, nil, err
		}
		screen, err := sessions.Read(ctx, args.ID, "")
		if err != nil {
			return nil, nil, err
		}
		return text(fmt.Sprintf("%s in %d pages | %s %s | %s", strings.Join(ids, ","), pages, got.Name, screen.Mode, screen.Output)), nil, nil
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
			mark := extension.Span{Text: "◈", Tone: extension.ToneAccent}
			rungs := []extension.Line{
				{mark, {Text: " 2c · 3/h · 12m"}},
				{mark, {Text: " 2c · 3/h"}},
				{mark},
			}
			widths := make([]string, 0, len(rungs))
			for _, rung := range rungs {
				widths = append(widths, fmt.Sprint(rung.Width()))
			}
			record("rungs.txt", strings.Join(widths, " "))
			n.ui.Decorate(s.ID, extension.Badge{Text: "noop:" + s.Status, Tone: extension.ToneAccent},
				extension.Badge{Rungs: rungs},
				extension.Badge{Text: "items", Tone: extension.ToneGood, Placement: extension.PlaceAfterName})
			n.ui.Group(s.ID, extension.Line{{Text: "noop head ", Tone: extension.ToneAccent, Bold: true}, {Text: s.ID}})
			n.ui.Own(s.ID, s.Status == "working")
			if s.ID == "c41d0001" {
				// Dead as its pane is, the child is blocked on the operator.
				n.ui.Attention(s.ID, extension.Attention{NeedsPerson: true, Rank: extension.RankBlocked})
			}
		}
	})
	// What the operator hands a session from the board, which is how an
	// extension that asked the operator something learns they answered.
	board.OnOperator(func(in extension.OperatorInput) {
		record("operator.txt", fmt.Sprintf("%s %s %v %q id=%d", in.SessionID, in.Via, in.Dialog, in.Text, in.MessageID))
	})
	// A line in the board's own log, with a credential-shaped value the
	// board must scrub before it reaches the file.
	board.Logger().Info("noop on the board", "note", "key sk-"+strings.Repeat("x", 24))
	// A span in the board's own trace, which goes wherever the board's
	// tracing does.
	board.Tracer().Start("start", slog.Int("sessions", 1)).End(nil)
	record("started.txt", fmt.Sprint(os.Getpid(), " ", board.ConfigDir()))
	switch os.Getenv("NOOP_SCENARIO") {
	case "quiet":
		return func() { record("stopped.txt", fmt.Sprint(ctx.Err() != nil)) }, nil
	case "orphan-hold":
		go n.orphanHold(ctx, board, dir)
		return func() { record("stopped.txt", fmt.Sprint(ctx.Err() != nil)) }, nil
	}
	// A worker of no role, supervised: its status is noop's to pin only
	// once it claims it, and the pin is what the board's passes then show.
	go func() {
		worker, err := board.Launch(ctx, extension.LaunchRequest{
			Tool: "envecho", Name: "supervised", Prompt: "work", ParentID: "c41d0001", Directory: dir,
		})
		if err != nil {
			record("supervised.txt", "error: "+err.Error())
			return
		}
		supervised := worker.ID
		if err := board.PinStatus(ctx, worker.ID, "waiting"); err != nil {
			supervised += " refused unclaimed"
		}
		if err := board.Supervise(ctx, worker.ID, true); err != nil {
			record("supervised.txt", "error: "+err.Error())
			return
		}
		if err := board.PinStatus(ctx, worker.ID, "waiting"); err != nil {
			record("supervised.txt", "error: "+err.Error())
			return
		}
		record("supervised.txt", supervised+" pinned")
	}()
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
		// Its helper's status is noop's to pin; the child's is not.
		pinned := "pinned"
		if err := board.PinStatus(ctx, helper.ID, "waiting"); err != nil {
			pinned = "error: " + err.Error()
		}
		if err := board.PinStatus(ctx, "c41d0001", "waiting"); err != nil {
			pinned += "; refused the child"
		}
		record("pinned.txt", pinned)
		// Ending the helper removes its status file, so the test reads the
		// pin first and says so before the helper is messaged and killed.
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(dir, "pin-read.txt")); err == nil {
				break
			}
		}
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
		// A second message queues behind the first, so it is still waiting
		// when the extension takes it back.
		if _, err := board.Send(ctx, helper.ID, extension.Message{Text: "and then this", Subject: "later"}); err != nil {
			record("withdrew.txt", "error: "+err.Error())
			return
		}
		withdrew, err := board.Withdraw(ctx, helper.ID, "later")
		if err != nil {
			record("withdrew.txt", "error: "+err.Error())
			return
		}
		record("withdrew.txt", fmt.Sprintf("%s withdrew %d", helper.ID, withdrew))
		// What an extension gathers for an agent goes inline up to the
		// extension's limit, and one byte over is refused before it queues.
		full := strings.Repeat("e", extension.MaxMessageBytes)
		if _, err := board.Send(ctx, helper.ID, extension.Message{Text: full, Subject: "events"}); err != nil {
			record("large.txt", "error: "+err.Error())
			return
		}
		_, err = board.Send(ctx, helper.ID, extension.Message{Text: full + "e", Subject: "events"})
		record("large.txt", fmt.Sprintf("%s full queued, over too large %v", helper.ID, errors.Is(err, extension.ErrMessageTooLarge)))
		// The same words in the operator's own voice: queued under a voice of
		// the extension's own, so the matching subject replaces nothing.
		voiced, err := board.Send(ctx, helper.ID, extension.Message{Text: "carry on", Subject: "note", AsOperator: true})
		if err != nil {
			record("voiced.txt", "error: "+err.Error())
			return
		}
		record("voiced.txt", fmt.Sprintf("%s voiced %v superseded %d", helper.ID, voiced.MessageID > sent.MessageID, voiced.Superseded))
		// The helper's pane never draws a prompt, so a tool command is
		// refused as not at one, and its viewport, with no jump-back
		// affordance configured, is never parked.
		// ReadPane says so first: AtPrompt is the reading Command acts on.
		pane, err := board.ReadPane(ctx, helper.ID)
		if err != nil {
			record("command.txt", "error: "+err.Error())
			return
		}
		err = board.Command(ctx, helper.ID, extension.ToolCommand{Text: "/model fast"})
		record("command.txt", fmt.Sprintf("%s at-prompt %v read %v", helper.ID, !errors.Is(err, extension.ErrNotAtPrompt), pane.AtPrompt))
		parked, err := board.Unpark(ctx, helper.ID)
		if err != nil {
			record("unparked.txt", "error: "+err.Error())
			return
		}
		record("unparked.txt", fmt.Sprintf("%s parked %v", helper.ID, parked))
		// Children first: the terminal the test opens under the helper, then
		// the operator's own terminal, which is out of the board's reach.
		endTerminals(ctx, board, helper.ID, record)
		// Plan the replacement first, on a tool that writes out what its
		// pane was started with, and note the board on either side of the
		// plan, which must not move it.
		again := extension.LaunchRequest{Tool: "envdump", Prompt: "again", Args: []string{"--one word"}}
		before := boardState(ctx, board)
		plan, err := board.PlanReplace(ctx, helper.ID, again)
		if err != nil {
			record("replaced.txt", "error: "+err.Error())
			return
		}
		planned, _ := json.Marshal(map[string]any{"plan": plan, "before": before, "after": boardState(ctx, board)})
		_ = os.WriteFile(filepath.Join(dir, "plan.json"), planned, 0o600)
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
		fresh, swap, err := board.Replace(ctx, helper.ID, again, extension.ReplaceOptions{Hold: true})
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
		// Its helper done with, the extension files it away; the dead child it
		// was launched under is not the extension's to file.
		if err := board.Archive(ctx, "c41d0001"); err == nil {
			record("archived.txt", "error: archived a session with no role of its own")
			return
		}
		if err := board.Archive(ctx, fresh.ID); err != nil {
			record("archived.txt", "error: "+err.Error())
			return
		}
		archived, err := board.Get(ctx, fresh.ID)
		if err != nil {
			record("archived.txt", "error: "+err.Error())
			return
		}
		record("archived.txt", fmt.Sprintf("%s archived %v", archived.ID, archived.Archived))
		// A hold the extension never settles, on the retired helper: the
		// board aborts it when it stops the extension.
		pending, _, err := board.Replace(ctx, helper.ID, extension.LaunchRequest{Name: "pending", Prompt: "wait"}, extension.ReplaceOptions{Hold: true})
		if err != nil {
			record("pending.txt", "error: "+err.Error())
			return
		}
		record("pending.txt", fmt.Sprintf("%s held-under %s", pending.ID, pending.ParentID))
	}()
	return func() { record("stopped.txt", fmt.Sprint(ctx.Err() != nil)) }, nil
}

// UI adds one key to the list, which records the row it was pressed on and
// opens a view of it, and one key on that view's screen; asks the board to
// confirm before opening each configured session; and keeps the host so
// StartBoard can badge every row it is told about.
func (n *noop) UI(host extension.UIHost) (extension.UI, error) {
	n.ui = host
	for _, id := range n.confirm {
		host.ConfirmOpen(id, "another party is driving "+id)
	}
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
			host.Alert(extension.Alert{Title: press.SessionID, Body: "marked\nby noop", Tone: extension.ToneWarn})
			host.Hide("c41d0001", true)
			host.Open("peek", &peekView{session: press.SessionID, dir: dir, host: host})
			return nil
		},
	}, {
		Screen: "peek",
		Action: "peek_note",
		Keys:   []string{"n"},
		Label:  "note the key",
	}, {
		Screen: "peek",
		Action: "peek_child",
		Keys:   []string{"d"},
		Label:  "open a child view",
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
	}}, Filters: []extension.Filter{{
		Action: "noop_roots",
		Keys:   []string{"Y"},
		Label:  "only sessions with no parent",
		Badge:  "roots",
		Keep:   func(s extension.SessionInfo) bool { return s.ParentID == "" },
	}}}, nil
}

// composeForm is a view with two fields, which records what it is submitted
// with and closes.
type composeForm struct{ dir string }

func (f *composeForm) Title() string { return "noop compose" }

func (f *composeForm) Render(width, height int) []extension.Line {
	return []extension.Line{{{Text: "write a note"}}}
}

func (f *composeForm) Fields() []extension.Field {
	return []extension.Field{
		{ID: "note", Label: "note", Value: "from"},
		{ID: "tag", Label: "tag"},
	}
}

func (f *composeForm) Key(key extension.ViewKey) bool {
	if key.Action != extension.ActionSubmit {
		return false
	}
	line := key.Field + " " + key.Values["note"] + " " + key.Values["tag"] + "\n"
	_ = os.WriteFile(filepath.Join(f.dir, "submitted.txt"), []byte(line), 0o600)
	return true
}

func (f *composeForm) Closed(reason extension.CloseReason) {
	recordClosed(f.dir, "compose", reason)
}

// peekView shows the row it was opened on, records every key it is told
// about, and opens a child view on peek_child.
type peekView struct {
	session, dir string
	host         extension.UIHost
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
	if key.Action == "peek_child" {
		v.host.Open("peek", &childView{parent: v})
	}
	return false
}

// endTerminals waits for a terminal to be nested under parent, ends it, and
// then tries the operator's own terminal the test filed under nobody.
func endTerminals(ctx context.Context, board extension.BoardHost, parent string, record func(name, line string)) {
	filter := extension.SessionFilter{ParentID: parent, IncludeTerminals: true}
	var shell extension.SessionInfo
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && shell.ID == ""; time.Sleep(50 * time.Millisecond) {
		list, err := board.List(ctx, filter)
		if err != nil {
			record("terminals.txt", "error: "+err.Error())
			return
		}
		for _, info := range list.Sessions {
			if info.Terminal {
				shell = info
			}
		}
	}
	if shell.ID == "" {
		record("terminals.txt", "no terminal under "+parent)
		return
	}
	record("terminals.txt", fmt.Sprintf("%s listed %v", shell.ID, shell.Running))
	ended, err := board.Kill(ctx, shell.ID)
	if err != nil {
		record("terminals.txt", "error: "+err.Error())
		return
	}
	record("terminals.txt", fmt.Sprintf("%s ended %s %v", ended.ID, ended.Status, ended.Running))
	if _, err := board.Kill(ctx, "0be7a001"); err != nil {
		record("terminals.txt", "0be7a001 refused")
	} else {
		record("terminals.txt", "0be7a001 ended")
	}
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
// boardState is every session's id and whether it runs, in order: what a
// launch or a kill would change, and a poll pass would not.
func boardState(ctx context.Context, board extension.Board) []string {
	list, err := board.List(ctx, extension.SessionFilter{IncludeArchived: true})
	if err != nil {
		return []string{"error: " + err.Error()}
	}
	var out []string
	for _, s := range list.Sessions {
		out = append(out, fmt.Sprintf("%s %v", s.ID, s.Running))
	}
	return out
}

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

// ShapeSpawn adds a prefix to every migration's prompt, and keeps a session
// named detached under the session that spawned it, with a prefix and a
// suffix of its own.
func (n *noop) ShapeSpawn(_ context.Context, launch extension.Launch) (extension.SpawnShape, error) {
	switch {
	case launch.Reason == extension.LaunchMigrate:
		return extension.SpawnShape{PromptPrefix: "NOOP PREFIX from " + launch.From}, nil
	case launch.Session.Name == "detached":
		return extension.SpawnShape{PromptPrefix: "NOOP PREFIX for " + launch.Session.SpawnedBy, PromptSuffix: "NOOP SUFFIX for " + launch.Session.SpawnedBy, KeepUnderSpawner: true}, nil
	}
	return extension.SpawnShape{}, nil
}

// AllowSpawn refuses a session named over-budget, the way a spawn budget
// would, and one named one-too-many while its parent has a
// live child already, counted on the board before the spawn.
func (n *noop) AllowSpawn(ctx context.Context, spawn extension.Spawn) error {
	switch spawn.Session.Name {
	case "over-budget":
		return errors.New("the spawn budget is spent")
	case "one-too-many":
		list, err := spawn.Sessions.List(ctx, extension.SessionFilter{ParentID: spawn.Session.ParentID})
		if err != nil {
			return err
		}
		var live []string
		for _, child := range list.Sessions {
			if child.Running {
				live = append(live, child.Name)
			}
		}
		if len(live) > 0 {
			return fmt.Errorf("%s already has live children: %s", spawn.Session.ParentID, strings.Join(live, ", "))
		}
	}
	return nil
}

func (n *noop) Spawned(_ context.Context, spawn extension.Spawn) {
	n.record("spawned.txt", fmt.Sprintf("%s %s %s", spawn.Session.ID, spawn.By, spawn.Session.Role))
}

// SessionFormFields adds one toggle to the board's new-session form.
func (n *noop) SessionFormFields() []extension.FormField {
	return []extension.FormField{{Key: "items", Label: "items", Kind: extension.FormToggle}}
}

// FormSpawned records each spawn from the new-session form with what its
// toggle was set to.
func (n *noop) FormSpawned(_ context.Context, sess extension.SessionInfo, form map[string]string) {
	n.record("formspawned.txt", sess.ID+" items="+form["items"])
}

// LaunchEnv tells every launch why it happened and where to say so; the
// tool the test launches writes the one into the other. A launch from the
// new-session form is also recorded with what its toggle was set to.
func (n *noop) LaunchEnv(_ context.Context, launch extension.Launch) (map[string]string, error) {
	dir, err := n.config.DataDir()
	if err != nil {
		return nil, err
	}
	if launch.Form != nil {
		n.record("form.txt", launch.Session.Name+" items="+launch.Form["items"])
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

// Commands adds noop-echo, which prints its arguments with the configured
// greeting, so a run shows the extension was configured before it,
// noop-who, which reads the board through the Host it is given, and
// noop-plan, which plans a replacement through that Host. The
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
	}, {
		Group: "Noop fixture",
		Name:  "noop-plan",
		Usage: "noop-plan <id>",
		About: "print what replacing a session would launch, without launching it",
		Run: func(ctx context.Context, args []string, host extension.Host) error {
			if len(args) != 1 {
				return errors.New("noop-plan needs a session id")
			}
			plan, err := host.PlanReplace(ctx, args[0], extension.LaunchRequest{Tool: "envecho", Prompt: "again"})
			if err != nil {
				return err
			}
			fmt.Printf("new id %v | carried %v | prompt %v\n", plan.SessionID != args[0],
				plan.Env["GATE_INBOX_SESSION_ID"] == plan.SessionID, strings.Contains(plan.Command, "again"))
			return nil
		},
	}}
	if os.Getenv("NOOP_FIXTURE_CLASH") != "" {
		commands = append(commands, extension.Command{Name: "task", Run: func(context.Context, []string, extension.Host) error { return nil }})
	}
	return commands
}

// Roles makes its helper a session the board keeps in view and speaks for
// the operator through: left out of its parent's send-children, its sends
// to its parent relayed as the operator's, its status pinned by noop.
func (n *noop) Roles() []extension.RoleSpec {
	return []extension.RoleSpec{{
		Name: "helper", OnScreen: true, FloatParent: true,
		SkipSendChildren: true, RelayToParent: true, PinnedStatus: true,
	}}
}

// Relay keeps a relay addressed to noop itself, and marks the rest as the
// operator's answer.
func (n *noop) Relay(_ context.Context, relay extension.Relay) (string, error) {
	n.record("relayed.txt", fmt.Sprintf("%s>%s %s", relay.From.ID, relay.To.ID, relay.Text))
	if relay.Text == "for noop" {
		return "", nil
	}
	return "the operator says: " + relay.Text, nil
}

// childView is opened from a peekView, and puts it back when the operator
// dismisses it.
type childView struct{ parent *peekView }

func (c *childView) Title() string { return "noop child" }

func (c *childView) Render(width, height int) []extension.Line {
	return []extension.Line{{{Text: "child of " + c.parent.session}}}
}

func (c *childView) Key(extension.ViewKey) bool { return false }

func (c *childView) Closed(reason extension.CloseReason) {
	recordClosed(c.parent.dir, "child", reason)
	if reason == extension.CloseDismissed {
		c.parent.host.Open("peek", c.parent)
	}
}

// recordClosed appends why a view was closed to closed.txt.
func recordClosed(dir, view string, reason extension.CloseReason) {
	f, err := os.OpenFile(filepath.Join(dir, "closed.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintf(f, "%s %s\n", view, reason)
		f.Close()
	}
}

// ToolDrivers teaches the board one agent CLI, styled "echo", that the core
// has no code for.
func (n *noop) ToolDrivers() []extension.ToolDriver {
	return []extension.ToolDriver{echoDriver{}}
}

// echoDriver registers the MCP server by leaving a note of what it was
// handed, captures a conversation id with the core's exported checks, and
// hands a conversation over by a path of its own making.
type echoDriver struct {
	extension.UnsupportedToolDriver
}

func (echoDriver) Style() string { return "echo" }

func (echoDriver) RegisterMCP(_ context.Context, req extension.MCPRequest) (extension.MCPLaunch, error) {
	if !req.DryRun {
		if err := os.MkdirAll(req.HooksDir, 0o755); err != nil {
			return extension.MCPLaunch{}, err
		}
		note := fmt.Sprintf("%s %s mcp %s=%s", req.ServerName, req.Executable, req.SessionIDEnv, req.SessionID)
		if err := os.WriteFile(filepath.Join(req.HooksDir, "echo-mcp-"+req.SessionID), []byte(note), 0o644); err != nil {
			return extension.MCPLaunch{}, err
		}
	}
	return extension.MCPLaunch{Env: map[string]string{"ECHO_MCP_SERVER": req.ServerName}}, nil
}

// echoRecord is the first line of an echo conversation file.
type echoRecord struct {
	ID      string    `json:"id"`
	Cwd     string    `json:"cwd"`
	Created time.Time `json:"created"`
}

// CaptureSession reads the conversation files echo keeps in the session's
// directory, with the core's own checks: an id not shaped like one is left
// alone, a directory is compared through its symlinks, and the earliest
// conversation left over is this session's.
func (echoDriver) CaptureSession(_ context.Context, req extension.CaptureRequest) (string, error) {
	paths, err := filepath.Glob(filepath.Join(req.Directory, "*.echo.jsonl"))
	if err != nil {
		return "", err
	}
	var cands []extension.SessionCandidate
	for _, path := range paths {
		rec, ok := readEchoRecord(path)
		if !ok || !extension.ValidSessionID(rec.ID) || !extension.SamePath(rec.Cwd, req.Directory) {
			continue
		}
		if rec.Created.Before(req.LaunchedAt) || req.Claimed(rec.ID) {
			continue
		}
		cands = append(cands, extension.SessionCandidate{ID: rec.ID, Created: rec.Created})
	}
	return extension.EarliestSession(cands), nil
}

func readEchoRecord(path string) (echoRecord, bool) {
	file, err := os.Open(path)
	if err != nil {
		return echoRecord{}, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return echoRecord{}, false
	}
	var rec echoRecord
	if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
		return echoRecord{}, false
	}
	return rec, true
}

func (echoDriver) MigrateTranscript(_ context.Context, req extension.TranscriptRequest) (extension.Transcript, error) {
	return extension.Transcript{Path: filepath.Join(req.Directory, req.ID+".echo.jsonl"), Format: "One echo record per line."}, nil
}

// Killed records every kill it hears of, the way an extension closing its
// own per-session state would.
func (n *noop) Killed(_ context.Context, kill extension.KillContext) {
	n.record("kills.txt", fmt.Sprintf("%s %s %s %s", kill.Session.ID, kill.Via, kill.By, kill.Session.Status))
}

// items is a second extension with one tool, so a test can refuse one
// extension's section and watch the other keep serving.
type items struct{ label string }

type itemsSettings struct {
	Label string `toml:"label"`
}

func (*items) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: "items", Version: "0.0.1"}
}

func (c *items) Configure(cfg extension.Config) error {
	s := itemsSettings{Label: "item"}
	if err := cfg.Decode(&s); err != nil {
		return err
	}
	c.label = s.Label
	return nil
}

func (c *items) RegisterMCP(r *extension.Registrar, _ extension.SessionContext) error {
	return extension.AddTool(r, &mcp.Tool{
		Name:        "items_draw",
		Description: "Answer with the configured item label.",
	}, func(context.Context, *mcp.CallToolRequest, pingArgs) (*mcp.CallToolResult, any, error) {
		return text(c.label), nil, nil
	})
}

// tally answers for one session: a line the operator sends the session
// named in its settings is, as far as it is concerned, the answer it was
// waiting for, and it says so to the sender. It notes each send's message
// id, which the board's later report of the same line carries.
type tally struct {
	answers string
	config  extension.Config
}

func (*tally) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: "tally", Version: "0.0.1"}
}

func (n *tally) Configure(cfg extension.Config) error {
	var s struct {
		Answers string `toml:"answers"`
	}
	if err := cfg.Decode(&s); err != nil {
		return err
	}
	n.answers = s.Answers
	n.config = cfg
	return nil
}

func (n *tally) OperatorSent(_ context.Context, send extension.OperatorSend) (string, error) {
	dir, err := n.config.DataDir()
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(dir, "sent.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(f, "%s id=%d\n", send.Session.ID, send.MessageID)
	if err := f.Close(); err != nil {
		return "", err
	}
	if n.answers != "" && send.Session.Name == n.answers {
		return "answered", nil
	}
	return "", nil
}

// UI adds one list filter that takes over a toggle stored under an older
// setting name and a key file action under an older action name.
func (*items) UI(extension.UIHost) (extension.UI, error) {
	return extension.UI{
		Filters: []extension.Filter{{
			Action: "items_view",
			Keys:   []string{"C"},
			Label:  "only root sessions",
			Badge:  "items",
			Keep:   func(s extension.SessionInfo) bool { return s.ParentID == "" },
		}},
		Aliases: extension.Aliases{
			Settings: []extension.SettingAlias{{From: "items_only", Filter: "items_view"}},
			Actions:  []extension.ActionAlias{{From: "items_filter", To: "items_view"}},
		},
	}, nil
}

// extra is a third extension with nothing but a spawn policy, which lets
// every spawn through while its section is well formed, so a test can
// refuse that section and watch the policy fail closed.
type extra struct {
	settings struct {
		Limit int `toml:"limit"`
	}
}

func (*extra) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: "extra", Version: "0.0.1"}
}

func (t *extra) Configure(cfg extension.Config) error {
	return cfg.Decode(&t.settings)
}

func (*extra) AllowSpawn(context.Context, extension.Spawn) error { return nil }

func (*extra) Spawned(context.Context, extension.Spawn) {}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// configDefaults is what this build's operators start from: the named
// account settings the core leaves for a distribution to bring, and the
// extension's greeting. An operator's own config.toml overrides any of it.
const configDefaults = `
[tools.claude]
account_env = "FIXTURE_TOKEN"
account_secret = "FIXTURE_{account}"
accounts_command = "echo FIXTURE_alice1; echo FIXTURE_bob2"

[extensions.noop]
greeting = "distribution greeting"
`

// supplied are the keys configDefaults fills in. Each has to be one the
// core leaves to a distribution, so a key it renames fails this build at
// start rather than quietly supplying nothing.
var supplied = []string{"tools.claude.account_env", "tools.claude.account_secret", "tools.claude.accounts_command"}

func main() {
	listed := map[string]bool{}
	for _, s := range app.DistributionSupplied() {
		listed[s.Key] = true
	}
	for _, key := range supplied {
		if !listed[key] {
			fmt.Fprintln(os.Stderr, "fixture: the core no longer leaves", key, "to a distribution")
			os.Exit(1)
		}
	}
	err := app.Run(context.Background(), os.Args[1:], app.Options{
		Extensions: []extension.Extension{&noop{}, artifacts.New(), &items{}, &tally{}, &extra{}},
		BuildInfo:  app.BuildInfo{Version: "0.0.0-fixture"},
		// FIXTURE_EXTRA_DEFAULTS stands in for a build whose defaults are
		// wrong, without a second fixture module.
		ConfigDefaults: configDefaults + os.Getenv("FIXTURE_EXTRA_DEFAULTS"),
		// A snippet this build supplies under every operator's own file.
		SnippetDefaults: []app.Snippet{
			{Key: "r", Label: "review the diff", Text: "review the diff for mistakes"},
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture:", err)
		os.Exit(app.ExitCode(err))
	}
}
