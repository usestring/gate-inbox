package app

import (
	"context"
	"time"

	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcpshared"
)

// sharedMCPEvery is how often the board checks the shared MCP server: one
// loopback GET, so a dead server is back within this long.
const sharedMCPEvery = 30 * time.Second

// keepSharedMCP restarts the shared MCP server (mcpshared) when it has
// stopped while sessions still point at it. A launch starts it, but only a
// launch: a server that crashed or was killed would otherwise leave every
// session launched against it without its tools until the next launch. It
// comes back on its recorded port, so those sessions' URL reaches it again.
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
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			// No recorded server is one no session was ever pointed at.
			url := mcpshared.URL(dir)
			if url == "" || mcpshared.Healthy(url) {
				continue
			}
			if restarted, err := mcpshared.Ensure(dir, launch.Executable()); err != nil {
				logging.Warn("shared MCP server down and could not be restarted", "url", url, logging.Err(err))
			} else {
				logging.Info("shared MCP server restarted", "url", restarted)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
