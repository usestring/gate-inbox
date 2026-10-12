package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/usestring/gate-inbox/internal/cli"
	"github.com/usestring/gate-inbox/internal/codexbind"
)

var codexClientHelp = cli.HelpSection{
	Title: "Codex clients",
	Commands: []cli.HelpEntry{{
		Usage: "codex-client [--server <private-unix-socket>] -- codex [args]",
		About: "bind a shared-daemon Codex client to its own pane for verified intentional shutdown",
	}},
}

func runCodexClient(ctx context.Context, args []string, home string) error {
	set := flag.NewFlagSet("codex-client [--server <private-unix-socket>] -- codex [args]", flag.ContinueOnError)
	set.SetOutput(os.Stdout)
	set.Usage = func() {
		fmt.Fprintln(set.Output(), "usage: gate-inbox codex-client [--server <private-unix-socket>] -- codex [args]")
	}
	server := set.String("server", "", "existing Codex app-server Unix socket")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *server == "" {
		codexHome := os.Getenv("CODEX_HOME")
		if codexHome == "" {
			userHome, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			codexHome = filepath.Join(userHome, ".codex")
		}
		*server = filepath.Join(codexHome, "app-server-control", "app-server-control.sock")
	}
	if !filepath.IsAbs(*server) {
		return errors.New("Codex server socket must be absolute")
	}
	return codexbind.Run(ctx, home, *server, set.Args())
}
