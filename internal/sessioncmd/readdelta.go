package sessioncmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// What a parent actually wants from a child, which is not a screenshot.
//
// A pane capture is the last fifty lines of a terminal. It is the right
// answer to "show me what the user would see" and the wrong answer to both
// questions a manager has: has this finished, and what has happened since I
// looked. The first is answered by a structured block rather than by reading
// prose for a verdict; the second by a cursor, so the second read of a child
// carries the gap instead of the screen again.
//
// Neither replaces the pane. A tool whose conversation this cannot read --
// anything that is not Claude Code, and any Claude session whose transcript
// is missing because saving was off -- gets the pane and a line saying why,
// because a manager that is handed an error where it expected a screen has
// lost a capability it used to have.

// ReadDigest is the child's state as something to branch on.
type ReadDigest struct {
	Status string `json:"status" jsonschema:"Gate Inbox status: starting, working, waiting, finished, idle, errored or dead"`
	// Question is the prompt of the dialog the session is holding, with its
	// options, or empty when nothing is being asked. A parent answers it with
	// answer_session.
	Question string `json:"question,omitempty" jsonschema:"question the session is waiting on, with its options; empty when nothing is being asked"`
	// Result is the session's most recent prose turn -- the nearest thing a
	// transcript holds to "what it concluded". Empty when the conversation
	// could not be read.
	Result string `json:"result,omitempty" jsonschema:"the session's most recent prose turn, which is the closest thing to its result"`
}

// cursorSeparator joins the conversation a cursor was taken from to the
// offset within it. The conversation id travels with the offset because an
// offset alone is a number that means something different in every file: a
// revived session resumes onto a new conversation, and a cursor handed back
// from the old one would otherwise be read as a position in the new one and
// return the wrong bytes with no sign that anything was wrong.
const cursorSeparator = "@"

func encodeCursor(conversationID string, offset int64) string {
	if conversationID == "" {
		return ""
	}
	return conversationID + cursorSeparator + strconv.FormatInt(offset, 10)
}

// decodeCursor splits a cursor, returning ok only for one that parses and
// names the conversation the caller is now reading.
func decodeCursor(cursor, conversationID string) (offset int64, ok bool) {
	at := strings.LastIndex(cursor, cursorSeparator)
	if at < 0 {
		return 0, false
	}
	if cursor[:at] != conversationID {
		return 0, false
	}
	offset, err := strconv.ParseInt(cursor[at+1:], 10, 64)
	if err != nil || offset < 0 {
		return 0, false
	}
	return offset, true
}

// readDelta assembles the transcript half of a read: the cursor to hand
// back, the conversation added since the caller's last one, and the turns
// the digest reads a result out of.
//
// ok false means there was no transcript to read at all and note says which
// of the several reasons it was. ok true with a note is a delta that was
// served but not exactly as asked -- a cursor that no longer fits, or a gap
// too large for one read -- and the note travels with it rather than
// throwing the delta away.
func (s *Sessions) readDelta(target store.Session, since string) (cursor string, delta convo.Delta, note string, ok bool) {
	if target.Tool != "claude" {
		return "", convo.Delta{}, "only Claude Code sessions keep a transcript this can read; a " + target.Tool + " session returns its pane", false
	}
	if target.AgentSessionID == "" {
		return "", convo.Delta{}, "no conversation id has been captured for this session yet, so there is no transcript to read", false
	}
	path := convo.TranscriptFor(s.claudeHome, target.AgentSessionID, target.Cwd)
	if path == "" {
		return "", convo.Delta{}, "no transcript on disk for conversation " + target.AgentSessionID + ", so transcript saving is off for this session", false
	}
	offset, resumable := decodeCursor(since, target.AgentSessionID)
	if since != "" && !resumable {
		// Not an error: a cursor from before a revive names a conversation
		// this session no longer holds, which is stale rather than malformed.
		// The caller gets a first read and a cursor that does fit.
		note = "cursor was taken from a different conversation, so this read starts from the end of the current one"
	}
	delta, err := convo.Since(path, offset)
	if err != nil {
		return "", convo.Delta{}, "transcript could not be read: " + err.Error(), false
	}
	if delta.Rewound && note == "" {
		note = "more was added than one read returns, so this window begins later than the cursor asked"
	}
	return encodeCursor(target.AgentSessionID, delta.Next), delta, note, true
}

// renderDelta is the conversation's new text as a reader wants it. It is
// bounded to 8KiB, and from the end,
// because a turn's conclusion is the part a parent came for.
func renderDelta(delta convo.Delta) string {
	out := make([]string, 0, len(delta.Prompts)+len(delta.Turns))
	for _, prompt := range delta.Prompts {
		out = append(out, "> "+prompt)
	}
	out = append(out, delta.Turns...)
	return dialog.Truncate(strings.Join(out, "\n\n"), dialog.MaxTurnBytes)
}

// digest is the structured block: the status the pane reads as now, the
// question it is holding, and the last thing the session said.
func (r *runtime) digest(target store.Session, pane string, running bool, delta convo.Delta) ReadDigest {
	block := ReadDigest{Status: target.Status}
	// Only a live pane, never a stored snapshot. A dead session's snapshot is
	// the screen it died on, so running the rules over it reports whatever it
	// was doing at the time -- a question it can no longer be asked, or
	// "working" for a session that stopped an hour ago.
	if running && pane != "" {
		if engine, err := status.NewEngine(r.cfg); err == nil {
			// The row's status is only as fresh as the last poll, and a
			// manager that is not running never polls. The pane is in hand
			// either way, so read it the way park does.
			if state, ok := engine.Match(target.Tool, pane); ok {
				block.Status = state
			}
		}
		if held, ok := dialog.Parse(pane); ok {
			block.Question = formatQuestion(held)
		}
	}
	block.Result = dialog.Truncate(delta.LastTurn(), dialog.MaxTurnBytes)
	return block
}

// formatQuestion is a dialog as one block of text: what was asked, then the
// choices, so a parent can pass one straight to answer_session.
func formatQuestion(held dialog.Dialog) string {
	parts := []string{strings.TrimSpace(held.Prompt)}
	for _, option := range held.Options {
		parts = append(parts, "- "+option)
	}
	if standing := held.Standing(); standing > 1 {
		parts = append(parts, fmt.Sprintf("(%d questions standing)", standing))
	}
	return strings.Join(parts, "\n")
}
