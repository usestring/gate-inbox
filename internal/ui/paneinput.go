package ui

import (
	"strings"
	"time"
	"unicode"

	"github.com/usestring/gate-inbox/extension"
)

// keyStamp is the key last handed a session's pane, for the next key's
// PaneInput.Ready: the session it went to, and when the board was done with
// it. drawn is whether the screen drawn after it has moved ready on to the
// end of that frame.
type keyStamp struct {
	session string
	ready   time.Time
	drawn   bool
}

// notePaneInput reports input that reached a session's pane to the board's
// observer, with its text masked and Ready filled in from the key before it.
func (m *Model) notePaneInput(in extension.PaneInput) {
	in.Text = maskText(in.Text)
	if in.Paste {
		m.lastKey = keyStamp{}
	} else {
		if m.lastKey.session == in.SessionID {
			in.Ready = m.lastKey.ready
		}
		m.lastKey = keyStamp{session: in.SessionID, ready: in.At}
	}
	if observer := m.viewObserver(); observer != nil {
		observer.PaneInput(in)
	}
}

// maskText is text as PaneInput carries it: every character but whitespace
// replaced by x, so the length and the word breaks survive and the
// characters do not. A password typed at a prompt that doesn't echo, or a
// pasted key, never leaves the board.
func maskText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return r
		}
		return 'x'
	}, text)
}

// noteKeyDrawn moves the last key's ready on to the end of the frame drawn
// after it. Bubble Tea draws the view after every message, and a key into a
// pane can cost a whole frame -- the caret relit, a scroll pulled back -- so
// the key waiting behind it reaches the board well after the one before,
// though just after the board was free.
func (m *Model) noteKeyDrawn() {
	if m.lastKey.session != "" && !m.lastKey.drawn {
		m.lastKey.ready = time.Now()
		m.lastKey.drawn = true
	}
}
