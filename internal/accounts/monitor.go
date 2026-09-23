package accounts

import (
	"errors"
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
			if err := recordActiveUsage(st, tools, running, stop); err != nil {
				logging.Warn("subscription usage monitor", logging.Err(err))
			}
		}
		// Once at start rather than a minute in: a board that has just
		// started, or restarted, reports its usage straight away.
		sample()
		ticker := time.NewTicker(usageTTL)
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

func recordActiveUsage(st *store.Store, tools map[string]config.Tool, running func(string) bool, stop <-chan struct{}) error {
	if _, err := currentPool(); errors.Is(err, ErrNoPool) {
		return nil
	} else if err != nil {
		return err
	}
	sessions, err := st.ListSessions(false)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, session := range sessions {
		select {
		case <-stop:
			return nil
		default:
		}
		tool := tools[session.Tool]
		if session.Account == "" || tool.AccountEnv != "CLAUDE_CODE_OAUTH_TOKEN" || !running(session.ID) {
			continue
		}
		account := Normalize(session.Account)
		now := time.Now()
		key := tool.AccountSecret + ":" + account
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := snapshot(st, tool, account); err != nil {
			tracing.Record("account.unavailable", now, time.Now(), err, tracing.Attr{Key: "account.lender", Value: account})
		}
	}
	return nil
}
