package ui

import (
	"os"
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
	"github.com/usestring/gate-inbox/internal/tmux"
)

const (
	backgroundWorkingInterval = 4 * time.Second
	backgroundStableInterval  = 15 * time.Second
	submissionStatusGrace     = 3 * time.Second
)

type paneBinding struct {
	tool, conversation, cwd, role, parent, spawnedBy string
	socket, pane, status, pinned                     string
	acked                                            bool
}

type hookStamp struct {
	size     int64
	modified time.Time
}

type paneSample struct {
	pid       int
	binding   paneBinding
	hook      hookStamp
	capture   tmux.Capture
	proc      sysstat.ProcStat
	procAt    time.Time
	clean     string
	questions []convo.AskQuestion
}

func (p *poller) paneBinding(sess store.Session) paneBinding {
	pinned, _ := p.pinnedStatus(sess.ID)
	return paneBinding{
		tool: sess.Tool, conversation: sess.AgentSessionID, cwd: sess.Cwd,
		role: sess.Role, parent: sess.ParentID, spawnedBy: sess.SpawnedBy,
		socket: sess.TmuxSocket, pane: sess.TmuxPaneID, status: sess.Status,
		pinned: pinned, acked: sess.Acked,
	}
}

func (p *poller) hookStamp(sess store.Session) hookStamp {
	if p.hooks == nil || p.statusSources[sess.Tool] != hooks.StatusSourceClaude && !sessionhooks.Role(sess.Role).PinnedStatus {
		return hookStamp{}
	}
	info, err := os.Stat(p.hooks.StatusFile(sess.ID))
	if err != nil {
		return hookStamp{}
	}
	return hookStamp{size: info.Size(), modified: info.ModTime()}
}

func (p *poller) paneInterval(sess store.Session) time.Duration {
	if sess.ParentID != "" || sess.SpawnedBy != "" {
		return p.interval
	}
	switch sess.Status {
	case status.Working, status.Background:
		return max(p.interval, backgroundWorkingInterval)
	case status.Idle, status.Waiting, status.Finished, status.Errored:
		return max(p.interval, backgroundStableInterval)
	default:
		return p.interval
	}
}

func (p *poller) paneDue(sess store.Session, sample paneSample, selectedID string, now time.Time, hook hookStamp, queued, force bool) bool {
	if force || sess.ID == selectedID || queued || len(sess.PendingInputs) > 0 || sample.capture.Err != nil ||
		sample.capture.At.IsZero() || sample.binding != p.paneBinding(sess) || sample.hook != hook {
		return true
	}
	p.mu.Lock()
	input, settled := p.operatorInputAt[sess.ID], p.settledAt[sess.ID]
	p.mu.Unlock()
	if input.After(sample.capture.At) || !settled.Before(sample.capture.At) && !settled.IsZero() {
		return true
	}
	// The first post-submit capture may precede the agent's redraw. Keep
	// sampling through the grace so that capture cannot hide the new turn.
	if !sess.OperatorAt.IsZero() && sample.capture.At.Before(sess.OperatorAt.Add(submissionStatusGrace)) {
		return true
	}
	if p.paneQuiet(sess, sample, now) {
		return false
	}
	return now.Sub(sample.capture.At) >= p.paneInterval(sess)
}

// quietCeiling is the longest a resting pane goes uncaptured on the strength
// of tmux saying nothing was written to it. It is a backstop, not a cadence:
// whatever a pass derives from a screen that has not changed is what it
// derived last time, so the only thing the recapture can catch is a reading
// of tmux's stamp this program got wrong.
const quietCeiling = time.Minute

// paneQuiet reports whether a resting pane has had no output since its last
// capture, so capturing it again would read the same screen and derive the
// same status.
//
// The capture is what made the board cost what it did. On a 209-pane box the
// pass read about 33 panes a second, most of them children -- which poll at
// the base interval whatever their status, so a parent waiting on one hears
// promptly -- sitting idle or finished with nothing new on screen.
//
// Only resting statuses qualify. A working pane's status moves with the
// clock as well as the screen: a region that stops changing ends the turn
// only after quietEndGrace, and that needs the passes in between. Idle,
// waiting, finished and errored are read off the screen alone, so an
// unchanged screen re-derives them unchanged. Everything else that can move
// a resting row without output -- a hook file, a queued message, operator
// input, the selection, a rebinding -- already makes the pane due above.
//
// The stamp is whole seconds, so output in the same second as the capture
// cannot be told from output just before it: the pane counts as quiet only
// when its newest output is in an earlier second than the capture started.
// A pane tmux gave no stamp for is never quiet.
func (p *poller) paneQuiet(sess store.Session, sample paneSample, now time.Time) bool {
	switch sess.Status {
	case status.Idle, status.Waiting, status.Finished, status.Errored:
	default:
		return false
	}
	activity, ok := p.paneActivity[sess.ID]
	if !ok || activity.IsZero() || sample.capture.At.IsZero() {
		return false
	}
	return activity.Unix() < sample.capture.At.Unix() && now.Sub(sample.capture.At) < quietCeiling
}

func sampledProc(previous paneSample, pid int, stat sysstat.ProcStat, now time.Time, ncpu int, memTotal uint64) sysstat.ProcStat {
	if !stat.OK {
		return stat
	}
	elapsed := now.Sub(previous.procAt).Seconds()
	if previous.pid == pid && previous.proc.OK && !previous.procAt.IsZero() && elapsed > 0.05 {
		delta := max(0, stat.CPUSeconds-previous.proc.CPUSeconds)
		stat.CPUPercent = sysstat.HostCPUFromDelta(delta, elapsed, ncpu)
	} else {
		stat.CPUPercent = sysstat.HostCPUPercent(stat.PCPU, ncpu)
	}
	stat.RamPercent = sysstat.HostRAMPercent(stat.RSS, memTotal)
	return stat
}
