package agentsession

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// ClaudeTranscriptAge is how long since the conversation last wrote an entry
// to its transcript, or false when there is none to read.
//
// The file's modification time is not that. A running Claude Code process
// touches its transcript about once an hour with nothing new in it: on
// 2026-10-14 a child whose last entry was at 20:26 had its transcript
// modified at 01:01 and 02:01, as had a dozen idle sessions days after their
// last entry. Read by mtime, no transcript ever went quiet for
// status.BackgroundTailTrust, so a turn that ended with "1 shell still
// running" read working for good and its parent was never told it stopped.
// So the age is taken from the newest entry's own timestamp, and from the
// modification time only when the tail holds no timestamped entry.
func ClaudeTranscriptAge(cwd, sessionID string, now time.Time) (time.Duration, bool) {
	if sessionID == "" {
		return 0, false
	}
	path, err := ClaudeTranscriptPath(cwd, sessionID)
	if err != nil {
		return 0, false
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, false
	}
	if written, ok := newestEntryTime(file, info.Size()); ok {
		return now.Sub(written), true
	}
	return now.Sub(info.ModTime()), true
}

// newestEntryTime is the latest timestamp on an entry in the transcript's
// tail. Metadata lines such as ai-title or permission-mode carry none.
func newestEntryTime(file *os.File, size int64) (time.Time, bool) {
	offset := max(0, size-claudeTailBytes)
	raw := make([]byte, size-offset)
	if _, err := file.ReadAt(raw, offset); err != nil {
		return time.Time{}, false
	}
	lines := strings.Split(string(raw), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	var newest time.Time
	for _, line := range lines {
		var entry struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if entry.Timestamp.After(newest) {
			newest = entry.Timestamp
		}
	}
	return newest, !newest.IsZero()
}
