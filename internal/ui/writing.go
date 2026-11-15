package ui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/usestring/gate-inbox/extension"
)

// writingIdleReset is how long a pane line may sit without a key before the
// next key is taken as the start of a new one. A line the operator left
// unsent and came back to much later is usually gone from the composer by
// then, and keeping its first key would stretch Compose over the whole gap.
const writingIdleReset = 10 * time.Minute

// lineWriting measures the line being typed into one focused pane, for
// OperatorWriting. It keeps counts, two timestamps and the lengths of the
// line's word and space runs, never the characters: the run lengths are
// what lets Backspace take a word back off the count.
type lineWriting struct {
	measured extension.OperatorWriting
	first    time.Time
	last     time.Time
	// runs is the line as alternating runs: a positive length is a run of
	// word characters, a negative one a run of spaces.
	runs []int
	// lastKey is when the board finished forwarding the last character
	// key, and run how many characters have come in one burst-paced run up
	// to it. The gap to the next key is read from the end of the last one's
	// handling, not its start: the first key of a burst can take a pane
	// capture, and the keys queued behind it would otherwise read as slow.
	lastKey time.Time
	run     int
}

// writingFor is the line being measured in a session's pane, started fresh
// when the last one has sat idle past writingIdleReset.
func (m *Model) writingFor(id string, at time.Time) *lineWriting {
	w := m.writing[id]
	if w != nil && at.Sub(w.last) <= writingIdleReset {
		return w
	}
	if m.writing == nil {
		m.writing = map[string]*lineWriting{}
	}
	w = &lineWriting{}
	m.writing[id] = w
	return w
}

// takeWriting hands back the measure of the line a submit just ended and
// forgets it, so the next key starts a new line.
func (m *Model) takeWriting(id string, at time.Time) extension.OperatorWriting {
	w := m.writing[id]
	delete(m.writing, id)
	if w == nil || at.Sub(w.last) > writingIdleReset {
		return extension.OperatorWriting{}
	}
	return w.finish(at)
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

// key records one key forwarded into the pane: at is when it arrived and
// done when the board had finished forwarding it.
func (w *lineWriting) key(at, done time.Time, text, name string) {
	if text == "" {
		w.edit(name)
		if w.first.IsZero() {
			return
		}
		w.tick(at)
		return
	}
	w.tick(at)
	n := utf8.RuneCountInString(text)
	w.measured.KeyChars += n
	if !w.lastKey.IsZero() && at.Sub(w.lastKey) < extension.OperatorBurstGap {
		w.run += n
	} else {
		w.endRun()
		w.run = n
	}
	w.lastKey = done
	w.write(text)
}

// paste records text pasted into the pane.
func (w *lineWriting) paste(at time.Time, text string) {
	w.tick(at)
	w.endRun()
	w.lastKey = time.Time{}
	w.measured.Pastes++
	w.measured.PasteChars += utf8.RuneCountInString(text)
	w.write(text)
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

// edit applies a key that carries no characters. Backspace takes one off
// the end, ctrl+w the word before the caret with the spaces after it, and
// ctrl+u the whole line, which is what each does in the line editors these
// agent CLIs draw. A newline key (alt+enter, ctrl+j) is a space between
// words. Anything else -- an arrow, a function key -- leaves the runs alone.
func (w *lineWriting) edit(name string) {
	switch strings.TrimPrefix(name, "M-") {
	case "BSpace":
		if name != "BSpace" {
			// alt+backspace deletes a word, the same as ctrl+w.
			w.deleteWord()
			return
		}
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
	case "C-w":
		w.deleteWord()
	case "C-u":
		w.runs = nil
	case "Enter", "C-j":
		w.write("\n")
	}
}

func (w *lineWriting) deleteWord() {
	if last := len(w.runs) - 1; last >= 0 && w.runs[last] < 0 {
		w.runs = w.runs[:last]
	}
	if last := len(w.runs) - 1; last >= 0 && w.runs[last] > 0 {
		w.runs = w.runs[:last]
	}
}

// wordsIn is the word count of a line the board holds whole.
func wordsIn(text string) int {
	return len(strings.Fields(text))
}
