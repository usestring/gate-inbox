package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcpreg"
	"github.com/usestring/gate-inbox/internal/opencode"
)

// runOpencodeAttach is what a launched opencode session's pane runs first
// (see mcpreg's opencode style): it attaches the TUI to the board's shared
// server, or, when the board is set to standalone or the shared server
// cannot be reached, gives it the private server it always had. Either way it
// ends by exec'ing the TUI in its own place, so the pane's process is the
// opencode the board watches.
//
//	gate-inbox opencode-attach [--model provider/model] [--auto] --server-config <path> -- opencode [args]
func runOpencodeAttach(ctx context.Context, args []string, configDir string) error {
	flags := flag.NewFlagSet(mcpreg.OpencodeAttachVerb, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	model := flags.String("model", "", "")
	auto := flags.Bool("auto", false, "")
	serverConfig := flags.String("server-config", "", "")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%s: %w", mcpreg.OpencodeAttachVerb, err)
	}
	argv := flags.Args()
	if len(argv) == 0 {
		return fmt.Errorf("%s: no opencode command after --", mcpreg.OpencodeAttachVerb)
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	attach := opencode.Attach{
		Home:         configDir,
		Exe:          exe,
		Row:          os.Getenv(hooks.EnvSessionID),
		Token:        os.Getenv(envname.SessionToken),
		Model:        *model,
		Auto:         *auto,
		ServerConfig: *serverConfig,
		Dir:          dir,
		Argv:         argv,
	}
	run, extra := attach.Standalone(), map[string]string(nil)
	cfg, err := config.LoadDir(configDir)
	if err == nil && cfg.OpenCode.SharedServer() && *serverConfig != "" {
		shared, env, err := attach.Shared(ctx)
		if err == nil {
			run, extra = shared, env
		} else {
			// The session still starts, on the private server it would have
			// had before the shared one existed; the operator sees why.
			logging.Warn("opencode shared server unavailable; running standalone", "session", attach.Row, "err", err)
			fmt.Fprintf(os.Stderr, "gate-inbox: shared opencode server unavailable, starting standalone: %v\n", err)
		}
	}
	return execTUI(run, extra)
}

// opencodeServeVerb is the verb runOpencodeServe answers.
const opencodeServeVerb = opencode.ServeVerb

// runOpencodeServe supervises the board's shared opencode server; see
// opencode.Supervise. EnsureServer starts it, never a person.
//
//	gate-inbox opencode-serve --home <config dir> --config <path> -- <opencode>
func runOpencodeServe(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet(opencode.ServeVerb, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	home := flags.String("home", "", "")
	config := flags.String("config", "", "")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%s: %w", opencode.ServeVerb, err)
	}
	if *home == "" || *config == "" || flags.NArg() != 1 {
		return fmt.Errorf("%s: needs --home, --config and the opencode binary", opencode.ServeVerb)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()
	return opencode.Supervise(ctx, *home, flags.Arg(0), *config)
}

func execTUI(argv []string, extra map[string]string) error {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	env := os.Environ()
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	err = syscall.Exec(bin, argv, env)
	return errors.Join(fmt.Errorf("exec %s", bin), err)
}
