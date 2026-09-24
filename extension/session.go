package extension

import (
	"path/filepath"
	"regexp"
	"time"
)

// sessionIDPattern is the shape a conversation id has in every store the
// board reads: a UUID, opencode's ses_ token, or any similar plain token.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidSessionID reports whether id has the shape the board accepts for a
// conversation id. An id arrives from a file or database the agent CLI owns
// rather than from the board, and it goes on to name a session and reach a
// command line, so one shaped like anything but a plain token is left where
// it was found. The board applies the same check to every id a
// ToolDriver's CaptureSession returns; a driver calls it to skip a bad id
// and keep looking, instead of returning one the board will drop.
func ValidSessionID(id string) bool {
	return sessionIDPattern.MatchString(id)
}

// SamePath reports whether a and b name the same directory once symlinks
// are resolved. A session launched through a symlinked path (macOS /tmp is
// /private/tmp) has to match the store entry its CLI wrote, which usually
// records the resolved path. A path that cannot be resolved is compared as
// written.
func SamePath(a, b string) bool {
	return resolvePath(a) == resolvePath(b)
}

func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// SessionCandidate is a conversation that could be the one a session's CLI
// minted: it ran in the session's directory, was created at or after the
// launch, and no other session has claimed it.
type SessionCandidate struct {
	// ID is the conversation id.
	ID string
	// Created is when the conversation was created, or its store entry's
	// write time when the store records nothing better.
	Created time.Time
}

// EarliestSession returns the ID of the candidate created first, or "" when
// there is none, which is CaptureSession's answer for no match yet. When
// several sessions launch in one directory, each CLI's conversation is
// written after its own launch, so the first one written after this
// launch is this session's; a later one belongs to a sibling. A tie goes to
// the candidate listed first, and a candidate with an empty ID is skipped.
func EarliestSession(candidates []SessionCandidate) string {
	var best *SessionCandidate
	for i := range candidates {
		c := &candidates[i]
		if c.ID == "" {
			continue
		}
		if best == nil || c.Created.Before(best.Created) {
			best = c
		}
	}
	if best == nil {
		return ""
	}
	return best.ID
}
