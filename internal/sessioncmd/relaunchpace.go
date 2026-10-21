package sessioncmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/launchpace"
	"github.com/usestring/gate-inbox/internal/status"
)

// pace takes target's turn in the relaunch queue every relaunch goes
// through (see launchpace), before anything is ended: a session waiting its
// turn keeps running where it is. wait blocks until the turn comes; without
// it a busy queue is refused with launchpace.ErrBusy, for a sweep that will
// look again on its next pass.
func (s *Sessions) pace(runtime *runtime, id string, wait bool) (launchpace.Slot, error) {
	p := s.pacer(runtime)
	if wait {
		return p.Wait(context.Background(), id)
	}
	slot, after, err := p.Try(id)
	if err != nil {
		return launchpace.Slot{}, fmt.Errorf("%w (next look in %s)", err, after.Round(time.Second))
	}
	return slot, nil
}

func (s *Sessions) pacer(runtime *runtime) *launchpace.Pacer {
	if s.newPacer != nil {
		return s.newPacer(runtime)
	}
	return launchpace.FromConfig(s.configDir, runtime.cfg.Relaunch, runtime.relaunchRested)
}

// relaunchRested says whether the session launched at since has come to
// rest at its prompt, read off its live pane. The row cannot say: a
// relaunch writes the tool's resting status before the CLI has drawn
// anything, and only the board's poll moves it after that. A row that is
// gone, dead or filed away, or a pane that has gone, has nothing left to
// wait for.
func (r *runtime) relaunchRested(id string, since time.Time) bool {
	sess, err := r.store.Get(id)
	if err != nil || sess.Archived || sess.Status == status.Dead {
		return true
	}
	if sess.AgentLaunchedAt.Before(since) {
		return false
	}
	if !r.driver.Exists(id) {
		return true
	}
	pane, err := r.driver.CapturePane(id)
	clean := ansi.Strip(pane)
	if err != nil || strings.TrimSpace(clean) == "" {
		return false
	}
	engine, err := status.NewEngine(r.cfg)
	if err != nil {
		return false
	}
	if r.cfg.Tools[sess.Tool].ActivityCutoff != "" {
		if _, drawn := engine.ActivityRegion(sess.Tool, clean); !drawn {
			return false
		}
	}
	state, _ := engine.Match(sess.Tool, clean)
	return state != status.Working && state != status.Starting
}
