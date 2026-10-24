package store

import (
	"time"

	"github.com/usestring/gate-inbox/internal/priority"
)

// SizingHold is a session's sizing tier (InheritedTier) as the board applies
// it: the tier its resources are sized at now and, while a drop is held
// back, the tier it falls to and when the wait for it began.
//
// A raise applies at once and a drop waits, so a tier lowered by mistake --
// the p key pressed once too often, a session filed into the wrong group --
// costs nothing if it is put back in time. Every size the tier sets reads the
// one hold, so the board's compaction and the CLI's own window can never
// disagree about which tier a session is on.
type SizingHold struct {
	Tier     priority.Tier `json:"tier"`
	LowersTo priority.Tier `json:"lowers_to,omitempty"`
	Since    time.Time     `json:"since,omitzero"`
}

// Lowering reports whether a drop is being held back.
func (h SizingHold) Lowering() bool { return !h.Since.IsZero() }

// Sizing is a hold as its readers see it: the tier now, and a held drop's
// tier and the time it applies, zero when none is held.
type Sizing struct {
	Tier     priority.Tier
	LowersTo priority.Tier
	LowersAt time.Time
}

// Sizing is h under a downgrade delay of delay.
func (h SizingHold) Sizing(delay time.Duration) Sizing {
	if !h.Lowering() {
		return Sizing{Tier: h.Tier}
	}
	return Sizing{Tier: h.Tier, LowersTo: h.LowersTo, LowersAt: h.Since.Add(delay)}
}

// Step is the hold after a pass that found the session's sizing tier at
// want. A first sighting takes want as it is: there is nothing yet to hold a
// drop from. A drop that keeps moving keeps the time its wait began.
func (h SizingHold) Step(want priority.Tier, known bool, now time.Time, delay time.Duration) SizingHold {
	if !known || want.Rank() <= h.Tier.Rank() {
		return SizingHold{Tier: want}
	}
	if !h.Lowering() {
		h.Since = now
	}
	h.LowersTo = want
	if !now.Before(h.Since.Add(delay)) {
		return SizingHold{Tier: want}
	}
	return h
}
