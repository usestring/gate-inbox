package ui

// What an extension can say about where a session stands in the operator's
// queue that its status cannot: that it needs a person while its pane says it
// is working, and where it sorts in triage. Pushed from any goroutine and
// read by the loop from a snapshot, like the other row marks.

import "github.com/usestring/gate-inbox/internal/status"

// Attention is one extension's claim on a session's place in the queue. The
// zero value is no claim.
type Attention struct {
	// NeedsPerson puts the session on the operator's queue -- triage, the
	// attention filter, the jumps to what is waiting -- whatever its status,
	// and even while an extension owns it.
	NeedsPerson bool
	// Rank replaces the status's place in triage's order.
	Rank AttentionRank
}

// AttentionRank is a place in triage's order. The zero value leaves the
// session's status to decide.
type AttentionRank int

const (
	AttentionByStatus AttentionRank = iota
	AttentionWaiting
	// AttentionBlocked is after the waiting sessions and before the errored
	// ones: blocked on a decision about the work rather than on a question
	// in front of somebody.
	AttentionBlocked
	AttentionErrored
	AttentionFinished
	AttentionIdle
)

// tier is the triage tier a rank stands for, or "" for none.
func (r AttentionRank) tier() string {
	switch r {
	case AttentionWaiting:
		return status.Waiting
	case AttentionBlocked:
		return triageBlocked
	case AttentionErrored:
		return status.Errored
	case AttentionFinished:
		return status.Finished
	case AttentionIdle:
		return status.Idle
	}
	return ""
}

// merge is two extensions' claims on one session as one: somebody is needed
// if either says so, and the more urgent rank wins.
func (a Attention) merge(b Attention) Attention {
	out := Attention{NeedsPerson: a.NeedsPerson || b.NeedsPerson, Rank: a.Rank}
	if tb := b.Rank.tier(); tb != "" && (a.Rank.tier() == "" || triageRank(tb) < triageRank(a.Rank.tier())) {
		out.Rank = b.Rank
	}
	return out
}

// Attention replaces owner's claim on one session's place in the queue. The
// zero Attention clears it.
func (b *ExtensionBridge) Attention(owner, sessionID string, claim Attention) {
	if claim.Rank.tier() == "" {
		claim.Rank = AttentionByStatus
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if claim == (Attention{}) {
		delete(b.attention[owner], sessionID)
	} else {
		if b.attention[owner] == nil {
			b.attention[owner] = map[string]Attention{}
		}
		b.attention[owner][sessionID] = claim
	}
	b.changedLocked()
}
