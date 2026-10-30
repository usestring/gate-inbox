package cli

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/usestring/gate-inbox/extension/cmdline"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/inbox"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

const usageInboxServe = "inbox serve [--listen <host:port>]"

func inboxSection() section {
	return groupSection("Remote inbox", "inbox",
		"take messages from outside the board, such as a phone's dictation, each as a new top-level router session",
		[]command{
			{name: "serve", usage: usageInboxServe, about: "serve POST /inbox on loopback until stopped; configured under [inbox], tokens from the environment or a 0600 file", run: configCommand(runInboxServe)},
		})
}

func runInboxServe(out io.Writer, args []string, _, configDir string) error {
	set := cmdline.NewFlagSet(usageInboxServe)
	listen := set.String("listen", "", "host:port to bind instead of [inbox] listen; keep it on loopback")
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return err
	}
	settings := cfg.Inbox
	if *listen != "" {
		settings.Listen = *listen
	}
	tokens, err := inbox.LoadTokens(settings, configDir)
	if err != nil {
		return err
	}
	brief, err := inbox.ReadBrief(settings)
	if err != nil {
		return err
	}
	spawner, err := newInboxSpawner(settings, newSessions(configDir))
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	server, err := inbox.New(settings, tokens, brief, spawner, log)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Serve(ctx)
}

// inboxSpawner starts each router in-process, through the same path as
// `gate-inbox spawn --nest=false` from the operator's own shell: top level,
// never under whichever session the server happened to be started from.
type inboxSpawner struct {
	sessions  sessionCommands
	tool      string
	group     string
	directory string
}

func newInboxSpawner(settings inbox.Config, sessions sessionCommands) (*inboxSpawner, error) {
	directory, err := settings.StartDirectory()
	if err != nil {
		return nil, err
	}
	return &inboxSpawner{sessions: sessions, tool: settings.SpawnTool(), group: settings.Group, directory: directory}, nil
}

func (s *inboxSpawner) Spawn(_ context.Context, spawn inbox.Spawn) (any, error) {
	nest := false
	group := s.group
	return s.sessions.Create("", sessioncmd.CreateSessionOptions{
		Tool:      s.tool,
		Name:      spawn.Name,
		Prompt:    spawn.Prompt,
		Group:     &group,
		Directory: s.directory,
		Nest:      &nest,
	})
}
