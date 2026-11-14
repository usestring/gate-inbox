package store

import (
	"strconv"
	"strings"
	"time"
)

const (
	// HibernateAfterSetting is the Settings choice for how long a session
	// rests before the board hibernates it. "off" turns the sweep off, a Go
	// duration sets the rest period, and empty defers to config.toml's
	// hibernate_after.
	HibernateAfterSetting = "hibernate_after"
	// HibernateMinSessionsSetting is the Settings choice for how many live
	// sessions the board keeps before it hibernates any. Empty defers to
	// config.toml's hibernate_min_sessions.
	HibernateMinSessionsSetting = "hibernate_min_sessions"
)

// HibernationPolicy is the hibernation the board applies: the stored
// Settings choice where there is one, else the config.toml default passed
// in. After zero means the sweep is off. A stored value that does not parse
// falls back to the default rather than switching hibernation on or off by
// accident.
func (s *Store) HibernationPolicy(defaultAfter time.Duration, defaultMin int) (after time.Duration, minSessions int, err error) {
	after, minSessions = defaultAfter, defaultMin
	rawAfter, err := s.Setting(HibernateAfterSetting)
	if err != nil {
		return after, minSessions, err
	}
	if parsed, ok := ParseHibernateAfter(rawAfter); ok {
		after = parsed
	}
	rawMin, err := s.Setting(HibernateMinSessionsSetting)
	if err != nil {
		return after, minSessions, err
	}
	if n, convErr := strconv.Atoi(strings.TrimSpace(rawMin)); convErr == nil && n >= 0 {
		minSessions = n
	}
	return after, minSessions, nil
}

// ParseHibernateAfter reads a stored rest period. ok is false for an empty
// or unreadable value, which means the config default applies.
func ParseHibernateAfter(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "":
		return 0, false
	case "off", "0":
		return 0, true
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// FormatHibernateAfter is the stored form of a rest period: "off" for zero.
func FormatHibernateAfter(d time.Duration) string {
	if d <= 0 {
		return "off"
	}
	return d.String()
}
