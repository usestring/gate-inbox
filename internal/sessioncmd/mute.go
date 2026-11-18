package sessioncmd

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// Mute sets or clears a session's persistent mute: the operator's "keep this
// out of my triage queue". Unlike the drain's ephemeral mark, which lapses
// as soon as the pane changes, this is stored on the row, so a muted session
// stays out of the queue across polls and restarts until it is unmuted. It
// has no end; Snooze is the mute that lifts on its own.
//
// It reaches the same field the board's mute key writes, from the other
// side: an agent or a script marks a session without a manager running, and
// the manager's next poll picks the flag up like any other row change.
func (s *Sessions) Mute(sessionID, targetID string, muted bool) (mutedSession Session, err error) {
	defer start("sessioncmd.mute", sessionAttr(targetID)).done(&err)
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Session{}, err
	}
	target, err := runtime.mutable(targetID)
	if err != nil {
		return Session{}, err
	}
	if err := runtime.store.SetMuted(target.ID, muted); err != nil {
		return Session{}, err
	}
	target.Muted, target.MutedUntil = muted, time.Time{}
	return runtime.currentInfo(target, target.ID == sessionID), nil
}

// Snooze mutes a session until until: a mute with an end. The board's poll
// lifts it at that time, or sooner if the session stops on a question or
// permission dialog or errors, since either needs the operator in a way it
// did not when they set it aside. A finished turn does not lift it: sitting
// out the turns in between is what a snooze is for. It replaces whatever
// mute or snooze the session already has.
func (s *Sessions) Snooze(sessionID, targetID string, until time.Time) (snoozed Session, err error) {
	defer start("sessioncmd.snooze", sessionAttr(targetID)).done(&err)
	if !until.After(time.Now()) {
		return Session{}, fmt.Errorf("a snooze has to end in the future, and %s has passed", until.Format(time.RFC3339))
	}
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.store.Close()
	if _, err := runtime.caller(sessionID); err != nil {
		return Session{}, err
	}
	target, err := runtime.mutable(targetID)
	if err != nil {
		return Session{}, err
	}
	if err := runtime.store.SetSnoozed(target.ID, until); err != nil {
		return Session{}, err
	}
	target.Muted, target.MutedUntil = true, until
	return runtime.currentInfo(target, target.ID == sessionID), nil
}

// snoozeEnd is a row's snooze end as the tools report it, empty for a row
// that is not snoozed.
func snoozeEnd(sess store.Session) string {
	if !sess.Muted || sess.MutedUntil.IsZero() {
		return ""
	}
	return sess.MutedUntil.Format(time.RFC3339)
}

// FormatMuteState is a mute's result as one line: what the session is now,
// and for a snooze when it comes back.
func FormatMuteState(session Session) string {
	switch {
	case !session.Muted:
		return "unmuted " + FormatSession(session)
	case session.MutedUntil != "":
		return "muted until " + session.MutedUntil + " " + FormatSession(session)
	}
	return "muted " + FormatSession(session)
}

// untilLayouts are the forms a snooze's end may be written in besides
// RFC3339, each read in the local zone.
var untilLayouts = []string{"2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"}

// ParseSnoozeEnd turns the two ways of naming a snooze's end into a time:
// a duration from now ("90m", "4h", "2d"), or a moment ("15:04" for the
// next time the clock reads that, a date, a date and time, or RFC3339).
// Exactly one of the two may be given; both empty is a mute with no end,
// which is the zero time.
func ParseSnoozeEnd(length, until string, now time.Time) (time.Time, error) {
	length, until = strings.TrimSpace(length), strings.TrimSpace(until)
	switch {
	case length != "" && until != "":
		return time.Time{}, errors.New("give a snooze a length or an end, not both")
	case length != "":
		d, err := parseSnoozeLength(length)
		if err != nil {
			return time.Time{}, err
		}
		return now.Add(d), nil
	case until == "":
		return time.Time{}, nil
	}
	end, err := parseSnoozeMoment(until, now)
	if err != nil {
		return time.Time{}, err
	}
	if !end.After(now) {
		return time.Time{}, fmt.Errorf("a snooze has to end in the future, and %q has passed", until)
	}
	return end, nil
}

// parseSnoozeLength reads a Go duration, plus whole days, which a snooze is
// often measured in and time.ParseDuration has no unit for.
func parseSnoozeLength(length string) (time.Duration, error) {
	var d time.Duration
	if days, ok := strings.CutSuffix(length, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("snooze length %q is not a duration such as 90m, 4h or 2d", length)
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(length); err != nil {
			return 0, fmt.Errorf("snooze length %q is not a duration such as 90m, 4h or 2d", length)
		}
	}
	if d <= 0 {
		return 0, fmt.Errorf("snooze length %q has to be more than zero", length)
	}
	return d, nil
}

func parseSnoozeMoment(until string, now time.Time) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339, until); err == nil {
		return at, nil
	}
	for _, layout := range untilLayouts {
		if at, err := time.ParseInLocation(layout, until, now.Location()); err == nil {
			return at, nil
		}
	}
	clock, err := time.ParseInLocation("15:04", until, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("snooze end %q is not a time: use 15:04, 2006-01-02, 2006-01-02 15:04 or RFC3339", until)
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
	if !at.After(now) {
		// A clock time already gone today means tomorrow's.
		at = at.AddDate(0, 0, 1)
	}
	return at, nil
}

// mutable resolves a session a mute may target. Any row on the board is
// fair game -- terminals included, since an idle shell is handed over by a
// drain like any other resting session.
func (r *runtime) mutable(id string) (store.Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return store.Session{}, fmt.Errorf("session_id is empty; call %s to get one", r.words.ListSessions)
	}
	sess, err := r.store.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, fmt.Errorf("session %s does not exist; call %s for current ids", id, r.words.ListSessions)
	}
	return sess, err
}
