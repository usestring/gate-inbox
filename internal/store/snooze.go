package store

import "errors"

// SnoozeSetting is the Experimental features switch for timed snooze: a
// mute that lifts on its own. It ships off. Off, nothing offers a snooze and
// SetSnoozed refuses one, but the board's poll still lifts a snooze set
// while it was on, at its time or when the session stops on a dialog or
// errors, so turning the switch off never leaves a session muted for good.
const SnoozeSetting = "experimental_timed_snooze"

// ErrSnoozeOff is SetSnoozed's refusal while SnoozeSetting is off.
var ErrSnoozeOff = errors.New("timed snooze is experimental: turn it on in Settings → Experimental features")

// SnoozeEnabled reports whether the Timed snooze experiment is on.
func (s *Store) SnoozeEnabled() bool {
	value, err := s.Setting(SnoozeSetting)
	return err == nil && value == "on"
}
