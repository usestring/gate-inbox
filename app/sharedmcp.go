package app

import (
	"context"
	"time"

	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcpshared"
)

// sharedMCPEvery is how often the board checks the shared MCP server: one
// loopback GET, so a dead server is back within this long, and a call a
// session makes during the gap is the only one that fails.
const sharedMCPEvery = 5 * time.Second

// sharedMCPStart is how long the board waits for the server it starts.
const sharedMCPStart = 5 * time.Second

// keepSharedMCP starts the shared MCP server (mcpshared) when the board
// starts, and again whenever it has stopped. A launch only waits a moment
// for the server (launch.sharedWait), so it has to be up before the first
// launch; and a server that crashed or was killed would otherwise leave
// every session launched against it without its tools. It comes back on
// its recorded port, so those sessions' URL reaches it again. Started from
// the board, it carries the board's environment, which is what a delegated
// launch through it passes on.
func keepSharedMCP(dir string, on bool) (stop func()) {
	if !on || underTempDir(dir) {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(sharedMCPEvery)
		defer ticker.Stop()
		// warned is the last failure logged, so a server that cannot start
		// (a squatted port) is logged once, not every check.
		warned := ""
		for {
			if url := mcpshared.URL(dir); !mcpshared.Healthy(dir, url) {
				if started, err := mcpshared.Ensure(dir, launch.Executable(), sharedMCPStart); err != nil {
					if err.Error() != warned {
						logging.Warn("shared MCP server down and could not be started", "url", url, logging.Err(err))
						warned = err.Error()
					}
				} else {
					logging.Info("shared MCP server started", "url", started)
					warned = ""
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
