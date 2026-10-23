package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/internal/codexinput"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func (p *poller) steerInbox(sess store.Session, msg store.InboxMessage) (bool, error) {
	key := messageSend(msg.ID)
	if p.sendInFlight(key) || !p.reserveSend(key) {
		return false, nil
	}
	text := p.envelope(sess, msg)
	deliver := p.steerCodex
	if deliver == nil {
		deliver = codexinput.Deliver
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var claimed bool
		var claimedAt time.Time
		outcome, err := deliver(ctx, sess.AgentSessionID, text, func() (bool, error) {
			current, err := p.store.Get(sess.ID)
			if err != nil || current.AgentSessionID != sess.AgentSessionID || current.Archived || current.Status == status.Dead {
				return false, err
			}
			claimedAt = time.Now()
			claimed, err = p.store.ClaimMessage(msg.ID, claimedAt)
			return claimed, err
		})
		if outcome == codexinput.NotSent {
			if claimed {
				if err := p.store.ReleaseMessageClaim(msg.ID, claimedAt); err != nil {
					p.mu.Lock()
					p.sendErr = errors.Join(p.sendErr, err)
					p.mu.Unlock()
				}
			}
			// An unavailable endpoint must not trigger another immediate poll.
			p.releaseSend(key)
			return
		}
		p.finishSend(sess, key, err, func(err error) error {
			switch outcome {
			case codexinput.Accepted:
				return p.recordInboxDelivery(sess, msg)
			case codexinput.Unconfirmed:
				return errors.Join(fmt.Errorf("Codex delivery to %s is unconfirmed: %w", sess.Name, err), p.store.MarkDropped(msg.ID, time.Now()))
			default:
				return fmt.Errorf("unexpected Codex delivery outcome: %d", outcome)
			}
		})
	}()
	return true, nil
}
