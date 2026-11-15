package ui

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/store"
)

// lineWriting measures the line being typed into one focused pane, for
// OperatorWriting. It keeps counts, timestamps and the lengths of the line's
// word and space runs, never the characters: the run lengths are what lets
// Backspace take a word back off the count.
//
// A line runs until the key that submits it or ctrl+c, and never times out:
// a line left half written stays in the session's composer, and the operator
// comes back to it. Active caps the pause, so only Compose counts the wait.
type lineWriting struct {
	measured extension.OperatorWriting
	first    time.Time
	last     time.Time
	// runs is the line as alternating runs: a positive length is a run of
	// word characters, a negative one a run of spaces.
	runs []int
	// ready is when the board was done with the last key: the end of the
	// frame drawn after it, or the key's arrival until that frame is drawn.
	// A key arriving within OperatorBurstGap of it was already waiting, and
	// run counts the characters that came that way, back to back.
	ready time.Time
	run   int
	// inDialog is whether the last characters typed or pasted went in while
	// the pane held a dialog, which says whose Enter sends the line. A key
	// that types nothing, such as the arrow that moves to a dialog's row,
	// leaves it.
	inDialog bool
}

// writingFor is the line being measured in a session's pane.
func (m *Model) writingFor(id string) *lineWriting {
	w := m.writing[id]
	if w == nil {
		if m.writing == nil {
			m.writing = map[string]*lineWriting{}
		}
		w = &lineWriting{}
		m.writing[id] = w
	}
	return w
}

// submitWriting ends the line a submitting key at at sent and hands back its
// measure, so the next key starts a new line. On a dialog the key is the
// dialog's answer: a printable pick is a choice, not writing, and Enter sends
// the line only when it was typed into the dialog. A line left in the
// composer before the dialog came up stays for the composer's own Enter.
func (m *Model) submitWriting(id string, at time.Time, dialog, enter bool) extension.OperatorWriting {
	w := m.writing[id]
	if w == nil || dialog && !(enter && w.inDialog) {
		return extension.OperatorWriting{}
	}
	delete(m.writing, id)
	return w.finish(at)
}

// notePaintedKey stamps the end of the frame drawn after a forwarded key on
// the line that key went to. Bubble Tea draws the view after every message,
// and the first key into a pane can cost a whole frame -- the caret relit, a
// scroll pulled back -- which would otherwise split a burst after its first
// key.
func (m *Model) notePaintedKey() {
	if m.keyFrame != nil {
		m.keyFrame.painted(time.Now())
		m.keyFrame = nil
	}
}

// pruneWriting drops the lines of sessions the board no longer holds, as
// pruneMutes does for marks: a draft left in a pane that died is never sent.
func (m *Model) pruneWriting(sessions []store.Session) {
	for id := range m.writing {
		if !slices.ContainsFunc(sessions, func(sess store.Session) bool { return sess.ID == id }) {
			delete(m.writing, id)
		}
	}
}

// key records a key forwarded into the pane at at: text is the characters it
// typed, or "" for a key that only edits or moves the caret, which name (its
// tmux name) then says. dialog is whether the pane held a dialog.
func (w *lineWriting) key(at time.Time, text, name string, dialog bool) {
	if w.ready.IsZero() || at.Sub(w.ready) >= extension.OperatorBurstGap {
		w.endRun()
	}
	w.ready = at
	if text == "" {
		w.edit(name)
		if !w.first.IsZero() {
			w.tick(at)
		}
		return
	}
	w.inDialog = dialog
	w.tick(at)
	n := utf8.RuneCountInString(text)
	w.measured.KeyChars += n
	w.run += n
	w.write(text)
}

// painted moves ready on to the end of the frame drawn after the last key.
func (w *lineWriting) painted(at time.Time) {
	w.ready = at
}

// paste records text pasted into the pane; dialog is as for key.
func (w *lineWriting) paste(at time.Time, text string, dialog bool) {
	w.tick(at)
	w.endRun()
	w.ready = time.Time{}
	w.inDialog = dialog
	w.measured.Pastes++
	w.measured.PasteChars += utf8.RuneCountInString(text)
	w.write(text)
}

// tick moves the clock on to an event at at, adding the gap since the last
// one to Active.
func (w *lineWriting) tick(at time.Time) {
	if w.first.IsZero() {
		w.first, w.last = at, at
		return
	}
	w.measured.Active += min(max(at.Sub(w.last), 0), extension.OperatorPauseCap)
	w.last = at
}

// endRun counts the characters of the run just ended as a burst when it was
// long enough to be one.
func (w *lineWriting) endRun() {
	if w.run >= extension.OperatorBurstRun {
		w.measured.BurstChars += w.run
	}
	w.run = 0
}

// finish closes the line at the key that submitted it.
func (w *lineWriting) finish(at time.Time) extension.OperatorWriting {
	if w.first.IsZero() {
		return extension.OperatorWriting{}
	}
	w.tick(at)
	w.endRun()
	out := w.measured
	out.Compose = max(at.Sub(w.first), 0)
	for _, run := range w.runs {
		if run > 0 {
			out.Words++
			out.Chars += run
		} else {
			out.Chars -= run
		}
	}
	return out
}

// write adds text to the end of the line's runs.
func (w *lineWriting) write(text string) {
	for _, r := range text {
		step := 1
		if unicode.IsSpace(r) {
			step = -1
		}
		if last := len(w.runs) - 1; last >= 0 && (w.runs[last] > 0) == (step > 0) {
			w.runs[last] += step
			continue
		}
		w.runs = append(w.runs, step)
	}
}

// edit applies a key that carries no characters, as the line editors these
// agent CLIs draw do: Backspace takes one off the end, ctrl+w and
// alt+backspace the word before the caret with the spaces after it, ctrl+u
// the whole line, and ctrl+c abandons it. alt+enter and ctrl+j start a new
// row, a space between words. Anything else -- an arrow, a function key --
// leaves the runs alone.
func (w *lineWriting) edit(name string) {
	switch name {
	case "BSpace":
		if last := len(w.runs) - 1; last >= 0 {
			if w.runs[last] > 0 {
				w.runs[last]--
			} else {
				w.runs[last]++
			}
			if w.runs[last] == 0 {
				w.runs = w.runs[:last]
			}
		}
	case "C-w", "M-BSpace":
		if last := len(w.runs) - 1; last >= 0 && w.runs[last] < 0 {
			w.runs = w.runs[:last]
		}
		if last := len(w.runs) - 1; last >= 0 && w.runs[last] > 0 {
			w.runs = w.runs[:last]
		}
	case "C-u":
		w.runs = nil
	case "C-c":
		*w = lineWriting{}
	case "M-Enter", "C-j":
		w.write("\n")
	}
}

// wholeLine is the measure of a line the board holds whole: its words and
// characters, counted the way a pane line's are.
func wholeLine(text string) extension.OperatorWriting {
	return extension.OperatorWriting{Words: len(strings.Fields(text)), Chars: utf8.RuneCountInString(text)}
}
