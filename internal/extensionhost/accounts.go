package extensionhost

import (
	"context"
	"time"

	"github.com/usestring/gate-inbox/extension"
)

// SessionAccount is the account an agent session runs on and its CLI's
// account settings.
func (b *Board) SessionAccount(ctx context.Context, id string) (string, extension.AccountTool, error) {
	if err := ctx.Err(); err != nil {
		return "", extension.AccountTool{}, err
	}
	return b.cmds.BoardAccount(id)
}

// SwitchAccount re-points an agent session at account and resumes it there.
func (b *Board) SwitchAccount(ctx context.Context, id, account string) (extension.SessionInfo, error) {
	if err := ctx.Err(); err != nil {
		return extension.SessionInfo{}, err
	}
	switched, err := b.cmds.BoardSwitchAccount(id, account)
	if err != nil {
		return extension.SessionInfo{}, err
	}
	return info(switched), nil
}

func (v *boardView) SessionAccount(ctx context.Context, id string) (string, extension.AccountTool, error) {
	return v.events.board.SessionAccount(ctx, id)
}

func (v *boardView) SwitchAccount(ctx context.Context, id, account string) (extension.SessionInfo, error) {
	return v.events.board.SwitchAccount(ctx, id, account)
}

func (v *boardView) SetQueueDeadline(id string, at time.Time) {
	v.events.setDeadline(v.owner, id, at)
}

// setDeadline records owner's deadline for id; zero clears it.
func (b *Events) setDeadline(owner, id string, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if at.IsZero() {
		delete(b.deadlines[owner], id)
		return
	}
	if b.deadlines == nil {
		b.deadlines = map[string]map[string]time.Time{}
	}
	if b.deadlines[owner] == nil {
		b.deadlines[owner] = map[string]time.Time{}
	}
	b.deadlines[owner][id] = at
}

// QueueDeadlines is each session's earliest deadline any extension set,
// for triage to break ties on.
func (b *Events) QueueDeadlines() map[string]time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]time.Time{}
	for _, byID := range b.deadlines {
		for id, at := range byID {
			if held, ok := out[id]; !ok || at.Before(held) {
				out[id] = at
			}
		}
	}
	return out
}

// dropDeadlines forgets everything owner set, when its view is released.
func (b *Events) dropDeadlines(owner string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.deadlines, owner)
}
