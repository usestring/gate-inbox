package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// tracePass records one poll pass as a span with its phases under it.
//
// It reads the same measurements the log line does, so a trace and a log of
// the same pass can never disagree, and it sits outside logPass's level gate
// on purpose: that gate keeps routine passes from burying a slow one in the
// log, while the whole value of a trace is having the routine ones to compare
// the slow one against.
func tracePass(started time.Time, msg tea.Msg, stat passStat) {
	if !tracing.Enabled() {
		return
	}
	ended := time.Now()
	attrs := []tracing.Attr{
		{Key: "sessions", Value: stat.sessions},
		{Key: "live", Value: stat.live},
	}
	for socket, count := range stat.failures {
		attrs = append(attrs, tracing.Attr{Key: "capture.failed." + socket, Value: count})
	}
	if _, isErr := msg.(errMsg); isErr {
		attrs = append(attrs, tracing.Attr{Key: "failed", Value: true})
	}
	trace := tracing.NewTrace("poll.pass", started, ended, attrs...)

	// The phases are consecutive laps of one clock, so each begins where the
	// last ended and the waterfall is the pass as it actually ran. A dotted
	// part is accumulated out of disjoint stretches of the phase above it and
	// has no such position, so it rides that phase as an attribute: a child
	// span with a start invented for it would read as a measurement.
	at := started
	for _, phase := range groupPhases(stat.phases) {
		trace.Child(phase.name, at, at.Add(phase.d), phase.parts...)
		at = at.Add(phase.d)
	}
	trace.Emit()
}

// phaseSpan is one top-level phase with the accumulated parts that belong to
// it already attached.
type phaseSpan struct {
	name  string
	d     time.Duration
	parts []tracing.Attr
}

// groupPhases folds each dotted part onto the phase it names, keeping the
// order walk reports so the spans come out in the order the pass ran them.
func groupPhases(phases passPhases) []phaseSpan {
	var grouped []phaseSpan
	for _, step := range phases.walk() {
		if step.part {
			if len(grouped) == 0 {
				continue
			}
			last := &grouped[len(grouped)-1]
			last.parts = append(last.parts, tracing.Attr{Key: step.name, Value: step.d})
			continue
		}
		grouped = append(grouped, phaseSpan{name: step.name, d: step.d})
	}
	return grouped
}
