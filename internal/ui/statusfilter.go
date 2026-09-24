// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import "github.com/usestring/gate-inbox/internal/store"

// statusFilter is which status set the list shows. The zero value is all
// sessions. To add a mode: define a const, append it to statusFilterCycle,
// and handle it in label and matches.
type statusFilter int

const (
	statusFilterAll statusFilter = iota
	statusFilterAttention
)

// statusFilterCycle is the order `w` walks. Append new modes before the
// wrap back to all happens at the end of next().
var statusFilterCycle = []statusFilter{
	statusFilterAll,
	statusFilterAttention,
}

func (f statusFilter) next() statusFilter {
	for i, mode := range statusFilterCycle {
		if mode == f {
			return statusFilterCycle[(i+1)%len(statusFilterCycle)]
		}
	}
	return statusFilterAll
}

func (f statusFilter) active() bool {
	return f != statusFilterAll
}

// label is the short badge word for the header and empty state, empty when
// the filter shows every status.
func (f statusFilter) label() string {
	switch f {
	case statusFilterAttention:
		return "attention"
	default:
		return ""
	}
}

// matches reports whether a session status is visible under this filter.
//
// A status is all it reads. The attention filter also keeps a parent whose
// child needs somebody, and that is a fact about other rows, so that half is
// attentionViaChild's -- which keeps this the pure function the whole cycle
// is written against.
func (f statusFilter) matches(st string) bool {
	switch f {
	case statusFilterAttention:
		return requiresInput(st)
	default:
		return true
	}
}

// attentionViaChild is the half of the attention filter that a status cannot
// answer: a session an extension says needs a person, and a parent whose
// child is waiting on one.
//
// It is not a nicety. A child is drawn under its parent, so a
// parent dropped by the filter takes its children off the list with it -- and
// a parent working away while its child sits on a question is exactly the
// case w exists to surface. It was dropped, and the question with it.
func (m *Model) attentionViaChild(sess store.Session) bool {
	if m.statusFilter != statusFilterAttention {
		return false
	}
	return m.extAttention[sess.ID].NeedsPerson || m.hasChildNeedingSomebody(sess.ID)
}

// hasChildNeedingSomebody reports a child of this session that a person has
// to answer.
func (m *Model) hasChildNeedingSomebody(parentID string) bool {
	if parentID == "" {
		return false
	}
	for _, sess := range m.sessions {
		if sess.ParentID != parentID || sess.Archived {
			continue
		}
		if m.needsPerson(sess) {
			return true
		}
	}
	return false
}
