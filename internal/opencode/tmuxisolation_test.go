package opencode

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain isolates this package's tests from any live tmux server, children
// included: see internal/tmuxtest. Run as "opencode serve" with the fake's
// variable set, the test binary is the stand-in server shared_test.go starts,
// and run with the supervisor's verb it is that server's supervisor.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == ServeVerb {
		flags := flag.NewFlagSet(ServeVerb, flag.ExitOnError)
		home, config := flags.String("home", "", ""), flags.String("config", "", "")
		_ = flags.Parse(os.Args[2:])
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		if Supervise(ctx, *home, flags.Arg(0), *config) != nil {
			os.Exit(1)
		}
		return
	}
	if os.Getenv(fakeServerEnv) != "" {
		fakeServe()
		return
	}
	tmuxtest.Main(m)
}
