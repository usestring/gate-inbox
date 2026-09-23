package ui

import (
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

func seedLimitBenchmark(st *store.Store, engine *status.Engine, sess store.Session, pane string, now, attempted time.Time) error {
	if sess.Tool != "codex" {
		return nil
	}
	state := store.LimitRecovery{
		Banner:  engine.LimitBanner(sess.Tool, ansi.Strip(pane)),
		ResetAt: now.Add(24 * time.Hour), LaunchAt: sess.LaunchTime(), AttemptedAt: attempted,
	}
	_, err := st.CompareLimitRecovery(sess, nil, &state)
	return err
}

func benchmarkLimitPoll(p *poller, sessions []store.Session, panes map[string]string, now time.Time) error {
	states, err := p.store.LimitRecoveries()
	if err != nil {
		return err
	}
	hashes := make(map[string]uint64, len(sessions))
	for _, sess := range sessions {
		clean := ansi.Strip(panes[sess.ID])
		derived, err := p.deriveCleanPaneStatus(sess, clean, true, hashes)
		if err != nil {
			return err
		}
		if _, err := p.maybeRecoverLimit(sess, states, tmux.Capture{Text: panes[sess.ID]}, clean, derived, true, true, now); err != nil {
			return err
		}
	}
	p.paneHashes = hashes
	return nil
}
