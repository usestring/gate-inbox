package ui

import (
	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// turnGate asks the build's turn gates whether the board may type message
// id into sess now. Tests replace gateTurn.
func (p *poller) turnGate(sess store.Session, id int64) extension.TurnVerdict {
	gate := p.gateTurn
	if gate == nil {
		gate = sessionhooks.GateTurn
	}
	return gate(sess, extension.TurnFromBoard, id)
}
