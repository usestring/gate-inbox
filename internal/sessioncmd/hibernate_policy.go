package sessioncmd

import (
	"cmp"
	"slices"
	"time"

	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
)

func NormalizeHibernateSensitivity(value string) string {
	switch value {
	case "low", "high", "off":
		return value
	default:
		return "balanced"
	}
}

func hibernateBudget(sensitivity string, memory sysstat.Snapshot, active int) int {
	sensitivity = NormalizeHibernateSensitivity(sensitivity)
	if sensitivity == "off" || !memory.MemOK || memory.MemTotal == 0 {
		return active
	}
	perSession := uint64(1 << 30)
	pressureAt := 80.0
	switch sensitivity {
	case "low":
		perSession /= 2
		pressureAt = 90
	case "high":
		perSession *= 2
		pressureAt = 70
	}
	// Reserve half the machine for the OS and other applications; session count estimates demand.
	budget := max(1, int(memory.MemTotal/2/perSession))
	if memory.MemPercent >= pressureAt {
		budget = min(budget, max(0, active-max(1, active/4)))
	}
	return budget
}

func (s *Sessions) HibernateAdaptive(sensitivity string, memory sysstat.Snapshot, dryRun bool) (result HibernateResult, err error) {
	defer start("sessioncmd.hibernate_adaptive").done(&err)
	result.DryRun = dryRun
	if NormalizeHibernateSensitivity(sensitivity) == "off" || !memory.MemOK {
		return result, nil
	}
	runtime, err := s.open()
	if err != nil {
		return result, err
	}
	defer runtime.store.Close()
	rows, err := runtime.store.ListSessions(false)
	if err != nil {
		return result, err
	}
	var live []store.Session
	for _, sess := range rows {
		if !sess.Archived && !runtime.cfg.Tools[sess.Tool].Shell && runtime.driver.Exists(sess.ID) {
			live = append(live, sess)
		}
	}
	remaining := len(live) - hibernateBudget(sensitivity, memory, len(live))
	slices.SortFunc(live, func(a, b store.Session) int {
		return cmp.Or(a.LastStatusAt.Compare(b.LastStatusAt), cmp.Compare(a.ID, b.ID))
	})
	now := time.Now()
	procs := onceTable(agentsession.ProcessTable)
	for _, sess := range live {
		if remaining <= 0 {
			break
		}
		// A short grace period prevents a newly idle or just-woken session from churning.
		if reason := s.hibernateBlocker(runtime, sess, rows, 5*time.Minute, now, procs); reason != "" {
			result.Held = append(result.Held, sess.ID+": "+reason)
			continue
		}
		before := len(result.Hibernated)
		s.hibernateOne(runtime, sess, &result)
		if len(result.Hibernated) > before {
			remaining--
		}
	}
	return result, nil
}
