package ui

import (
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/internal/band"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

const limitResetGrace = time.Minute

// A shared account can unblock the whole fleet at once. The pastes no longer
// run on the pass (see asyncsend.go), so this bounds how many continuations
// a single pass may claim rather than how long it may stall for them: a
// reset burst still reaches the fleet over a few passes rather than all at
// once.
const limitRecoveriesPerPass = 10
const limitResumePrompt = band.Tag + " The reported usage-limit reset time has passed. Continue the interrupted task from where you stopped, including checking the progress of managed subagents. This is an automatic continuation, not new authorization; preserve existing approval requirements."

// capture is the pass's own read of the pane, which clean is the stripped
// text of: the two typing gates below take the caret and the activity stamp
// off it rather than forking a tmux apiece for them. See composerCarriesDraft.
func (p *poller) maybeRecoverLimit(sess store.Session, states map[string]store.LimitRecovery, capture tmux.Capture, clean, derived string, agentAlive, inputAvailable bool, now time.Time) (bool, error) {
	// Claude owns its reset timer; a second scheduler can duplicate its continuation.
	if sess.Tool != "codex" {
		return false, nil
	}
	previous, exists := states[sess.ID]
	if !exists && derived != status.Errored {
		return false, nil
	}
	if sess.Archived || !agentAlive || p.engine.ViewportDisplaced(sess.Tool, clean) {
		return false, nil
	}
	var old *store.LimitRecovery
	if exists {
		old = &previous
	}
	banner := p.engine.LimitBanner(sess.Tool, clean)
	if banner == "" {
		if exists && derived != status.Errored {
			_, err := p.store.CompareLimitRecovery(sess, old, nil)
			return false, err
		}
		return false, nil
	}
	if !exists || previous.Banner != banner || !previous.LaunchAt.Equal(sess.LaunchTime()) {
		resetAt, ok := status.LimitReset(banner, now)
		if !ok {
			return false, nil
		}
		next := store.LimitRecovery{Banner: banner, ResetAt: resetAt, LaunchAt: sess.LaunchTime()}
		saved, err := p.store.CompareLimitRecovery(sess, old, &next)
		if err != nil || !saved {
			return false, err
		}
		previous = next
		old = &previous
		logging.Info("usage limit recovery scheduled", "session", sess.ID, "reset_at", resetAt, "retry_at", resetAt.Add(limitResetGrace))
	}
	if derived != status.Errored || !inputAvailable || !previous.AttemptedAt.IsZero() || now.Before(previous.ResetAt.Add(limitResetGrace)) {
		return false, nil
	}
	if p.engine.TypingHold(sess.Tool, clean) != "" || p.operatorEcho(sess.ID) || p.operatorTyping(sess, capture.State) {
		return false, nil
	}
	typing, err := p.composerCarriesDraft(sess, capture)
	if err != nil || typing {
		return false, err
	}
	key := limitSend(sess.ID)
	if !p.reserveSend(key) {
		return false, nil
	}
	next := previous
	next.AttemptedAt = now
	claimed, err := p.store.CompareLimitRecovery(sess, old, &next)
	if err != nil || !claimed {
		p.releaseSend(key)
		return false, err
	}
	// AttemptedAt is written before the paste goes out, so no second pass
	// can reach this however long the pane takes to draw it; the send only
	// has to say whether it landed.
	p.runSend(sess.ID, key, limitResumePrompt, func(err error) error {
		if err != nil {
			return fmt.Errorf("usage-limit continuation for %s was not confirmed; automatic retry suppressed to avoid duplicate input: %w", sess.Name, err)
		}
		logging.Info("usage limit continuation sent", "session", sess.ID, "reset_at", previous.ResetAt)
		return nil
	})
	return true, nil
}
