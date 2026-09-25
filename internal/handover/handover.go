// Package handover filters what a replacement session reads.
//
// A migration points the taking-over agent at the source's full transcript,
// which carries a predecessor's pollution into a fresh context: a stuck
// agent's transcript is wall-to-wall loop spam and decline essays, and read
// back whole, the replacement re-derives the same stuck behaviour.
//
// The filter is a transformation of what the replacement reads, never of the
// record itself. The transcript on disk is untouched; a filtered copy is
// written beside it. What it removes is the pollution,
// not the fact of it: a decline turn becomes a one-line marker saying a
// decline happened here, loop spam is collapsed, and everything before the
// last compaction boundary is dropped because the summary record at that
// boundary already carries it. The replacement starts informed rather than
// anchored.
package handover

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// Stats reports what a filter removed, for the note the replacement reads.
type Stats struct {
	// Dropped is how many records were loop spam: identical to a record
	// already seen in the recent window, so keeping the first is keeping
	// the information.
	Dropped int
	// Stubbed is how many turns read as a decline of the task. The essay
	// is replaced by one line saying a decline happened here.
	Stubbed int
	// Compacted says the filter cut to the record's last compaction
	// boundary; the summary record there carries everything before it.
	Compacted bool
	// Kept is how many records the filtered output holds.
	Kept int
}

// Empty reports whether nothing at all was removed, which is when a caller
// can hand over the original without a note.
func (s Stats) Empty() bool {
	return s.Dropped == 0 && s.Stubbed == 0 && !s.Compacted
}

// Options tunes one filter pass.
type Options struct {
	// KeepTo, when set, ends the copy at that line index inclusive and
	// drops everything after: the rewind case. The deviation point is the
	// last turn that was still on course, and the drift after it is what a
	// rewind exists to remove. It also skips the compaction-boundary cut:
	// a summary record written after the deviation describes the drift,
	// and cutting to it would hand that drift back as context. A boundary
	// that precedes the deviation still applies, because the summary there
	// describes on-course work.
	KeepTo *int
}

// DefaultOptions filters from the last compaction boundary, which is the
// migration case.
func DefaultOptions() Options { return Options{} }

// normalize folds text for comparison: lowercase, single spaces, no line
// breaks. A manager quoting a worker's turn reproduces the words, not the
// terminal's wrapping.
func normalize(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// DeviationCut finds where a rewind's filtered copy starts: the record whose
// text contains snippet, searched from the end, or found=false when nothing
// matches. The manager quotes the last turn that was still on course, so the
// copy keeps it and drops everything after; a snippet that appears in later
// drift cuts late, which the manager prompt warns about.
func DeviationCut(kind, path, snippet string) (cut int, found bool, err error) {
	snippet = normalize(snippet)
	if snippet == "" {
		return 0, false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	line := 0
	for {
		raw, rerr := reader.ReadBytes('\n')
		atEOF := rerr == io.EOF
		if len(raw) == 0 && atEOF {
			break
		}
		if rerr != nil && !atEOF {
			return 0, false, rerr
		}
		if recordSays(kind, raw, snippet) {
			cut, found = line, true
		}
		line++
		if atEOF {
			break
		}
	}
	return cut, found, nil
}

// recordSays reports whether one transcript record's conversation text
// contains the (already normalized) snippet, dispatched on the layout.
func recordSays(kind string, raw []byte, snippet string) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	switch kind {
	case "claude":
		return claudeRecordSays(trimmed, snippet)
	case "codex":
		return codexRecordSays(trimmed, snippet)
	}
	return false
}

// Note is the line a handover prompt carries about what was removed. It is
// addressed to the replacement: it says the file is filtered, what was
// filtered, and that the marker is the information.
func (s Stats) Note() string {
	note := "This transcript has been filtered for the handover, so read only what is here:"
	if s.Compacted {
		note += " everything before the last compaction summary is dropped (that summary carries it);"
	}
	if s.Dropped > 0 {
		note += fmt.Sprintf(" %d records of a repeated loop are collapsed;", s.Dropped)
	}
	if s.Stubbed > 0 {
		note += fmt.Sprintf(" %d turns that declined the task are reduced to one-line markers;", s.Stubbed)
	}
	return note[:len(note)-1] + "."
}

// loopWindow is how many recent records a repeat can match. A stuck agent
// repeats a turn within a handful of records; a genuinely new turn that
// happens to resemble one from an hour ago is not loop spam.
const loopWindow = 24

// recent is the ring of record identities the loop check matches against.
type recent struct {
	hashes [loopWindow]string
	at     int
}

// seen reports whether the identity is in the window, then records it.
func (r *recent) seen(hash string) bool {
	for _, h := range r.hashes {
		if h == hash {
			return true
		}
	}
	r.hashes[r.at%loopWindow] = hash
	r.at++
	return false
}

// declineStub replaces a declined turn. It is the information, not the
// essay: what happened, and that nothing here is being hidden -- a
// replacement that wants the essay can ask the operator for it.
const declineStub = "[filtered for handover: the worker declined the task here]"

// truncate marks where a kept string was cut. A tool result past the cap is
// almost always loop bulk; the marker says how much is missing rather than
// pretending the read was whole.
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + fmt.Sprintf("…[truncated, %d more characters]", len(text)-limit)
}
