package extensionhost

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

// Supervision is held here, in the board process, rather than in the store:
// only a board extension supervises, and it claims again at every
// StartBoard, so a claim that outlived the board would only be one nothing
// was keeping any more.

// PinnedStatus is the status an extension has pinned a session it
// supervises at, for the poll pass to show over what the pane reports.
func (b *Events) PinnedStatus(id string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.pins[id]
	return state, ok
}

// OnPinChange sets what asks the board for a pass, called whenever a
// supervised session's pin changes so the board shows it at once.
func (b *Events) OnPinChange(refresh func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refresh = refresh
}

// supervise records owner as supervising id, unless another extension
// already does.
func (b *Events) supervise(owner, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if other, taken := b.supervising[id]; taken && other != owner {
		return fmt.Errorf("session %s is supervised by extension %q, so it is not %q's to supervise", id, other, owner)
	}
	if b.supervising == nil {
		b.supervising = map[string]string{}
	}
	b.supervising[id] = owner
	return nil
}

// unsupervise lets id go, and the pin with it. A session nobody supervises
// is already let go; one another extension supervises is refused.
func (b *Events) unsupervise(owner, id string) error {
	b.mu.Lock()
	other, taken := b.supervising[id]
	if taken && other != owner {
		b.mu.Unlock()
		return fmt.Errorf("session %s is supervised by extension %q, not %q", id, other, owner)
	}
	_, pinned := b.pins[id]
	delete(b.supervising, id)
	delete(b.pins, id)
	refresh := b.refresh
	b.mu.Unlock()
	if pinned && refresh != nil {
		refresh()
	}
	return nil
}

// supervisedBy reports whether owner supervises id.
func (b *Events) supervisedBy(owner, id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.supervising[id] == owner
}

// pin holds id at state for owner, or releases it when state is "".
func (b *Events) pin(owner, id, state string) error {
	b.mu.Lock()
	if b.supervising[id] != owner {
		b.mu.Unlock()
		return fmt.Errorf("session %s is no longer supervised by extension %q", id, owner)
	}
	if state == "" {
		delete(b.pins, id)
	} else {
		if b.pins == nil {
			b.pins = map[string]string{}
		}
		b.pins[id] = state
	}
	refresh := b.refresh
	b.mu.Unlock()
	if refresh != nil {
		refresh()
	}
	return nil
}

// dropSupervision lets go of everything owner supervises, when its view is
// released: a stopped extension holds no worker still.
func (b *Events) dropSupervision(owner string) {
	b.mu.Lock()
	dropped := false
	for id, other := range b.supervising {
		if other != owner {
			continue
		}
		if _, pinned := b.pins[id]; pinned {
			dropped = true
		}
		delete(b.supervising, id)
		delete(b.pins, id)
	}
	refresh := b.refresh
	b.mu.Unlock()
	if dropped && refresh != nil {
		refresh()
	}
}

func (v *boardView) Supervise(ctx context.Context, id string, on bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !on && v.events.supervisedBy(v.owner, id) {
		return v.events.unsupervise(v.owner, id)
	}
	sess, err := v.Get(ctx, id)
	if err != nil {
		return err
	}
	if !on {
		return v.events.unsupervise(v.owner, sess.ID)
	}
	if owner, _, _ := strings.Cut(sess.Role, "/"); sess.Role != "" && owner != v.owner {
		return fmt.Errorf("session %s was launched by extension %q for a role of its own, so it is not %q's to supervise", sess.ID, owner, v.owner)
	}
	// Under the view's lock, so a claim cannot land after release has let
	// go of this extension's claims.
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.released {
		return errors.New("this extension has stopped on the board, so it supervises nothing")
	}
	return v.events.supervise(v.owner, sess.ID)
}

func (v *boardView) PinStatus(ctx context.Context, id, status string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	supervised := v.events.supervisedBy(v.owner, id)
	if !supervised {
		// id may be a prefix or a name; the claim is on the full id.
		if sess, err := v.Get(ctx, id); err == nil && v.events.supervisedBy(v.owner, sess.ID) {
			id, supervised = sess.ID, true
		}
	}
	if !supervised {
		return v.events.board.PinStatusFor(ctx, v.owner, id, status)
	}
	state, err := sessioncmd.PinnableStatus(status)
	if err != nil {
		return err
	}
	return v.events.pin(v.owner, id, state)
}
