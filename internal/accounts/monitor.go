package accounts

import (
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

func StartUsageMonitor(st *store.Store, tools map[string]config.Tool, running func(string) bool) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		reader := promptcache.NewReader(promptcache.DefaultRoot())
		last := map[string]contextSnapshot{}
		sample := func() {
			if !tracing.Enabled() {
				return
			}
			if err := recordSessionUsage(st, tools, running, reader, last); err != nil {
				logging.Warn("session usage monitor", logging.Err(err))
			}
		}
		// Once at start rather than a minute in: a board that has just
		// started, or restarted, reports its usage straight away.
		sample()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return func() { close(stop); <-done }
}
