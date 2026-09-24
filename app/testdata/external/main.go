// A build of the board from outside this module, carrying one extension of
// its own. It imports the public app and extension packages and nothing
// under internal/: the go tool refuses an internal import across modules,
// so this building at all is the proof that the public packages are enough.
// app's boundary test builds it as its own module and drives each of the
// executable's three faces.
package main

import (
	"bufio"
	"context"
	"encoding/json"
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
