package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

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
	server := set.String("server", "", "explicit existing Codex app-server Unix socket")
	if err := set.Parse(args); err != nil {
		return err
	}
	argv := set.Args()
	if codexUtility(argv) {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	autoStart := *server == ""
	if autoStart && len(argv) > 0 {
		help, err := exec.CommandContext(ctx, argv[0], "--help").Output()
		if err != nil {
			return fmt.Errorf("inspect Codex launch support: %w", err)
		}
		if legacyPrivateCodex(string(help)) {
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return cmd.Run()
		}
	}
	if autoStart {
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
	return codexbind.Run(ctx, home, *server, argv, autoStart)
}

func codexUtility(argv []string) bool {
	for n := 1; n < len(argv); n++ {
		arg := argv[n]
		switch arg {
		case "--":
			return false
		case "--help", "-h", "--version", "-V":
			return true
		case "-c", "--config", "-m", "--model", "-p", "--profile", "-s", "--sandbox", "-a", "--ask-for-approval", "-C", "--cd", "-i", "--image", "--enable", "--disable", "--remote", "--remote-auth-token-env", "--add-dir":
			n++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return slices.Contains([]string{"exec", "e", "review", "login", "logout", "mcp", "plugin", "app-server", "remote-control", "app", "completion", "update", "doctor", "sandbox", "debug", "apply", "queue", "archive", "delete", "migrate-rollouts", "unarchive", "cloud", "exec-server", "features", "help"}, arg)
	}
	return false
}

func legacyPrivateCodex(help string) bool {
	return strings.Contains(help, "Codex CLI") && !strings.Contains(help, "--remote") && !strings.Contains(help, "--no-daemon")
}
