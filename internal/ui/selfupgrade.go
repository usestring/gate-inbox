package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/logging"
)

// BinaryUpgraded is sent when a different build has replaced the binary the
// board was started from. A rebuilt binary on disk does nothing to a board
// already running: the process keeps the code it launched with until it is
// restarted, so a fix could sit installed for hours while the board kept the
// old behaviour. On this message the board quits and the app re-execs the new
// build in the same process and pane. Sessions live in tmux and carry on.
type BinaryUpgraded struct{}

// upgradeRetry re-checks whether the operator has gone idle enough to restart.
type upgradeRetry struct{}

const (
	// upgradeIdle is how long the operator must not have touched the board
	// before it restarts under them. The restart redraws the whole screen, and
	// a key pressed during it is lost.
	upgradeIdle = 30 * time.Second
	// upgradeRetryEvery is how often a deferred restart checks again.
	upgradeRetryEvery = 15 * time.Second
)

// RestartRequested reports whether the board quit to run a newer build.
func (m *Model) RestartRequested() bool { return m.restartForUpgrade }

// restartWhenIdle quits for the new build once that cannot cost the operator
// anything: the board is on its list, not searching, and untouched for
// upgradeIdle. Any other screen (a focused session, a form, a dialog) can
// hold typed text the restart would throw away, so it waits instead.
func (m *Model) restartWhenIdle() (tea.Model, tea.Cmd) {
	if !m.upgradeSafe(time.Now()) {
		return m, tea.Tick(upgradeRetryEvery, func(time.Time) tea.Msg { return upgradeRetry{} })
	}
	logging.Info("restarting the board on a new build")
	m.restartForUpgrade = true
	return m, tea.Quit
}

func (m *Model) upgradeSafe(now time.Time) bool {
	return m.mode == modeList && !m.searching && now.Sub(m.lastInteractionAt) >= upgradeIdle
}
