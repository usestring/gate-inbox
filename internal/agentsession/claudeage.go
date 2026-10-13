package agentsession

import (
	"os"
	"time"
)

// ClaudeTranscriptAge is how long since the conversation's transcript was
// last written, or false when there is none to read.
func ClaudeTranscriptAge(cwd, sessionID string, now time.Time) (time.Duration, bool) {
	if sessionID == "" {
		return 0, false
	}
	path, err := ClaudeTranscriptPath(cwd, sessionID)
	if err != nil {
		return 0, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return now.Sub(info.ModTime()), true
}
