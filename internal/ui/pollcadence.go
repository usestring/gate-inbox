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
	return now.Sub(sample.capture.At) >= p.paneInterval(sess)
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
