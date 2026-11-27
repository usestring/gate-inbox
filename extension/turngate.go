package extension

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TurnGate is implemented by an extension that must be asked before an agent
// session starts a turn: one that keeps a session off a resource it may no
// longer use, or moves it elsewhere first, such as onto another account.
//
// The host asks at the two places a turn starts: when the board is about to
// type a queued message into the pane (TurnFromBoard), and in the CLI's
// prompt-submit hook, for a prompt the operator or the CLI itself submitted
// (TurnFromPrompt). A gate that lets the turn through returns the zero
// TurnVerdict.
//
// A gate fails open. One that errors, panics or does not answer within the
// host's bound is skipped and the turn goes ahead, as if the build had no
// gate: a gate exists to delay or redirect a turn, and a broken one must not
// stop a session from working. The board asks from its poll loop, so a gate
// must answer quickly there; work that takes longer, such as relaunching the
// session elsewhere, belongs in the background, with Hold returned until it
// is done.
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

// Turn is one turn about to start. It carries no prompt text: a gate decides
// on the session, not on what it is told.
type Turn struct {
	Session SessionInfo
	Source  TurnSource
	// MessageID is the queued message for a turn from the board; 0 for a
	// prompt.
	MessageID int64
}

// TurnVerdict is what a gate decided. The zero value lets the turn start.
type TurnVerdict struct {
	// Hold keeps the turn from starting now. From the board, the message
	// stays queued and is offered to the gates again on a later pass. From
	// a prompt-submit hook, the prompt is blocked and Reason is shown.
	Hold bool
	// Requeue, with Hold on a prompt, queues the blocked prompt for the
	// session, so the board types it in once the gates let a turn through
	// (after the gate has moved the session elsewhere, say). It means
	// nothing for a turn from the board, whose message is still queued.
	Requeue bool
	// Reason is one line on why the turn is held, shown to the operator.
	Reason string
}

// maxTurnReasonBytes bounds a held turn's reason: a line, not a report.
const maxTurnReasonBytes = 300

// GateTurn asks every gate, in registration order, whether turn may start.
// The first that holds it decides; the gates after it are not asked. Each
// gate gets until bound to answer; one that errors, panics or runs past it
// is skipped (it fails open), and its failure comes back joined for the host
// to log. A nil SessionHooks lets every turn start.
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
