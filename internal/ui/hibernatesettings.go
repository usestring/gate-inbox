package ui

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// hibernateAfterChoices are the rest periods the Settings row steps
// through. Zero is off. A value set in config.toml that is not one of these
// joins the list, so the row can always show and return to it.
var hibernateAfterChoices = []time.Duration{
	0,
	30 * time.Minute,
	time.Hour,
	2 * time.Hour,
	4 * time.Hour,
	8 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
	72 * time.Hour,
}

// hibernateMinChoices are the live-session floors the Settings row steps
// through. Zero sets no floor.
var hibernateMinChoices = []int{0, 5, 10, 15, 20, 30, 50, 75, 100}

// withChoice is choices with current added in order when it is missing.
func withChoice[T int | time.Duration](choices []T, current T) []T {
	if slices.Contains(choices, current) {
		return choices
	}
	out := append(slices.Clone(choices), current)
	slices.Sort(out)
	return out
}

// stepChoice moves current one step through choices, wrapping at the ends.
func stepChoice[T int | time.Duration](choices []T, current T, step int) T {
	choices = withChoice(choices, current)
	index := slices.Index(choices, current)
	return choices[(index+step+len(choices))%len(choices)]
}

// hibernateAfterLabel is the rest-period row's value.
func hibernateAfterLabel(d time.Duration) string {
	if d <= 0 {
		return "off"
	}
	return "idle " + shortDuration(d)
}

// hibernateMinLabel is the floor row's value. The floor does nothing while
// hibernation is off, and the row says so rather than reading as in force.
func hibernateMinLabel(n int, after time.Duration) string {
	value := "no minimum"
	if n > 0 {
		value = strconv.Itoa(n) + " live sessions"
	}
	if after <= 0 {
		value += mutedStyle.Render("  (hibernation off)")
	}
	return value
}

// shortDuration writes a rest period the way a person would say it: 30m,
// 2h, 3d.
func shortDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

// persistHibernation saves the two hibernation rows and puts them in force.
// A choice equal to config.toml's clears the stored value instead, so a
// later config edit still reaches a board whose operator never changed it.
func (m *Model) persistHibernation() {
	after := ""
	if m.settings.hibernateAfter != m.cfg.HibernateAfter.Duration {
		after = store.FormatHibernateAfter(m.settings.hibernateAfter)
	}
	if err := m.store.SetSetting(store.HibernateAfterSetting, after); err != nil {
		m.errBar.text = "saving hibernate after: " + err.Error()
		return
	}
	minSessions := ""
	if m.settings.hibernateMinSessions != m.cfg.HibernateMinSessions {
		minSessions = strconv.Itoa(m.settings.hibernateMinSessions)
	}
	if err := m.store.SetSetting(store.HibernateMinSessionsSetting, minSessions); err != nil {
		m.errBar.text = "saving hibernate minimum: " + err.Error()
		return
	}
	m.hibernateAfter = m.settings.hibernateAfter
	m.hibernateMinSessions = m.settings.hibernateMinSessions
}
