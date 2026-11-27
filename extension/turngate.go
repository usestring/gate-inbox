package extension

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TurnGate is implemented by an extension that is asked before a session
// starts a turn: when the board is about to type a queued message
// (TurnFromBoard) and in the CLI's prompt-submit hook (TurnFromPrompt). The
// zero TurnVerdict lets the turn through.
//
// Gates fail open: one that errors, panics or misses the host's bound is
// skipped and the turn goes ahead. The board asks from its poll loop, so
// slow work belongs in the background, with Hold returned until it is done.
type TurnGate interface {
	GateTurn(ctx context.Context, turn Turn) (TurnVerdict, error)
}

// TurnSource is where a turn is about to start.
type TurnSource string

const (
	// TurnFromBoard is the board about to type a queued message into the
	// session's pane.
	TurnFromBoard TurnSource = "board"
	// TurnFromPrompt is the CLI's prompt-submit hook: a prompt has been
	// submitted and the CLI waits on the hook before running the turn.
	TurnFromPrompt TurnSource = "prompt"
)

// Turn is one turn about to start. It carries no prompt text.
type Turn struct {
	Session SessionInfo
	Source  TurnSource
	// MessageID is the queued message for a turn from the board; 0 for a
	// prompt.
	MessageID int64
}

// TurnVerdict is what a gate decided. The zero value lets the turn start.
type TurnVerdict struct {
	// Hold keeps the turn from starting. A board message stays queued; a
	// prompt is blocked and Reason is shown.
	Hold bool
	// Requeue, with Hold on a prompt, queues the prompt so the board types
	// it in once the gates let a turn through. Ignored for board turns.
	Requeue bool
	// Reason is one line on why the turn is held, shown to the operator.
	Reason string
}

// maxTurnReasonBytes bounds a held turn's reason: a line, not a report.
const maxTurnReasonBytes = 300

// GateTurn asks every gate, in registration order, whether turn may start;
// the first that holds it decides. A gate that errors, panics or runs past
// bound is skipped and its error returned joined. A nil SessionHooks lets
// every turn start.
func (h *SessionHooks) GateTurn(ctx context.Context, turn Turn, bound time.Duration) (TurnVerdict, error) {
	if h == nil {
		return TurnVerdict{}, nil
	}
	var errs []error
	for _, g := range h.gates {
		verdict, err := askGate(ctx, g.v, turn, bound)
		if err != nil {
			errs = append(errs, fmt.Errorf("extension %q: %w", g.id, err))
			continue
		}
		if verdict.Hold {
			verdict.Reason = strings.Join(strings.Fields(verdict.Reason), " ")
			if len(verdict.Reason) > maxTurnReasonBytes {
				verdict.Reason = strings.ToValidUTF8(verdict.Reason[:maxTurnReasonBytes], "")
			}
			if verdict.Reason == "" {
				verdict.Reason = "extension " + g.id + " is holding this turn"
			}
			if turn.Source != TurnFromPrompt {
				verdict.Requeue = false
			}
			return verdict, errors.Join(errs...)
		}
	}
	return TurnVerdict{}, errors.Join(errs...)
}

// errGateTimeout is a gate that did not answer within its bound.
var errGateTimeout = errors.New("did not answer in time; the turn went ahead")

// askGate runs one gate under bound. A gate that ignores its context is left
// to finish on its own; its late answer is dropped.
func askGate(ctx context.Context, gate TurnGate, turn Turn, bound time.Duration) (TurnVerdict, error) {
	if bound <= 0 {
		bound = time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	type answer struct {
		v   TurnVerdict
		err error
	}
	done := make(chan answer, 1)
	go func() {
		var v TurnVerdict
		err := guard(func() error {
			var err error
			v, err = gate.GateTurn(ctx, turn)
			return err
		})
		done <- answer{v, err}
	}()
	select {
	case a := <-done:
		return a.v, a.err
	case <-ctx.Done():
		return TurnVerdict{}, errGateTimeout
	}
}
