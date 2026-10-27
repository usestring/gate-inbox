package ui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/logging"
)

// Arrivals: an agent started outside the board announces the pane it runs
// in (hooks/arrivals.go), and the board scans just that pane within about a
// second, rather than finding it on the next full scan up to 45 seconds
// later. The announcement also settles what the pane is: the agent that
// made it is in the pane's own process tree, which is as good as a command
// match, so a pane the full scan would have passed over as "not confident"
// is taken.

// arrivalPollEvery is how often the board looks for announcements. A read
// of one directory, nearly always empty, so it can be frequent.
const arrivalPollEvery = time.Second

type arrivalTickMsg struct{}

type arrivalsState struct {
	// queued is the announcements read but not yet scanned, held while
	// another scan is out.
	queued []hooks.Arrival
}

// arrivalClaim is what one announcement lets the scan assume about a pane.
type arrivalClaim struct {
	pid  int
	tool string
}

// arrivalStart prepares the directory the hooks write into and starts the
// poll. A board with no hooks manager has nothing to read.
func (m *Model) arrivalStart() tea.Cmd {
	if m.hooks == nil {
		return nil
	}
	if err := m.hooks.PrepareArrivals(); err != nil {
		logging.Warn("arrivals directory not made", logging.Err(err))
		return nil
	}
	return m.arrivalTick()
}

func (m *Model) arrivalTick() tea.Cmd {
	return tea.Tick(arrivalPollEvery, func(time.Time) tea.Msg { return arrivalTickMsg{} })
}

// scanArrivals reads what has been announced and scans those panes. Only
// one scan runs at a time, so while one is out the announcements wait.
func (m *Model) scanArrivals(now time.Time) tea.Cmd {
	if m.hooks == nil {
		return nil
	}
	m.arrivals.queued = append(m.arrivals.queued, m.hooks.TakeArrivals(now)...)
	if len(m.arrivals.queued) == 0 || m.adoptBusy {
		return nil
	}
	arrivals := m.arrivals.queued
	m.arrivals.queued = nil
	if m.outsidePanesMode() == paneIgnore {
		return nil
	}
	run := m.newAdoptRun()
	if run == nil {
		return nil
	}
	tool := m.arrivalTool()
	m.adoptBusy = true
	return func() tea.Msg {
		started := time.Now()
		candidates, claims := locateArrivals(arrivals, tool)
		run.arrivals = claims
		return run.scan(candidates, started, false)
	}
}

// locateArrivals reads each announced pane from its server, keeping only
// the ones still on the server that announced them.
func locateArrivals(arrivals []hooks.Arrival, tool string) ([]adopt.Candidate, map[string]arrivalClaim) {
	claims := map[string]arrivalClaim{}
	var candidates []adopt.Candidate
	for _, arrival := range arrivals {
		key := adoptKey(arrival.Socket, arrival.PaneID)
		if _, seen := claims[key]; seen {
			claims[key] = arrivalClaim{pid: arrival.AgentPID, tool: tool}
			continue
		}
		candidate, server, ok := adopt.PaneAt(arrival.Socket, arrival.PaneID)
		if !ok || server != arrival.ServerPID {
			logging.Info("an announced pane is gone", "socket", arrival.Socket, "pane", arrival.PaneID)
			continue
		}
		claims[key] = arrivalClaim{pid: arrival.AgentPID, tool: tool}
		candidates = append(candidates, candidate)
	}
	return candidates, claims
}

// arrivalTool is the tool an announcement speaks for. Only Claude Code's
// hooks announce, so it is the configured tool whose status comes from
// them, "claude" when there are several.
func (m *Model) arrivalTool() string {
	var names []string
	for name, tool := range m.cfg.Tools {
		if !tool.Shell && tool.StatusSource == hooks.StatusSourceClaude {
			names = append(names, name)
		}
	}
	if slices.Contains(names, "claude") {
		return "claude"
	}
	slices.Sort(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// arrived adds an announcement's evidence to what identification found. The
// announcement counts only while the agent that made it is still in the
// pane's own process tree: a pane whose agent exited and gave its id to
// something else is not the pane that spoke.
func (r *adoptRun) arrived(candidate adopt.Candidate, match adopt.Match, ok bool, procs *adopt.ProcTable) (adopt.Match, bool) {
	claim, found := r.arrivals[adoptKey(candidate.Socket, candidate.PaneID)]
	if !found || claim.tool == "" || !slices.Contains(procs.PIDs(candidate.PID), claim.pid) {
		return match, ok
	}
	if !ok || !match.Confident() {
		match = adopt.Match{Candidate: candidate, Tool: claim.tool, Signals: match.Signals}
	}
	match.Signals = append(match.Signals, adopt.SignalArrival)
	return match, true
}
