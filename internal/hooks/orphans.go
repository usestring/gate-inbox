package hooks

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OrphanGrace is how long a session's file must have gone unwritten before
// the sweep may take it. A launch can write its first file before its row is
// listed, and a minute-old file is not one a sweep should race; an hour is
// far past any launch and still clears a deleted session the same day.
const OrphanGrace = time.Hour

// PruneOrphans removes the files deleted sessions left in the hooks
// directory: each session's status log and the stamps beside it, its pending
// AskUserQuestion call, its exit code and its conversation pointer. Remove
// takes only the status file, because a killed session can be revived and
// keeps the rest, and a session deleted from the store (by the archive
// sweep, a failed launch, or the CLI) has no single exit that knows about
// every file. On 2026-11-14 one board held 2,175 such files for 34 sessions.
//
// A file is a session's when the part of its name before the first dot is a
// session id: lowercase hex, eight characters or more. Shared files
// (claude-settings-*.json, the MCP configs) and directories never match.
// live names every row the store still holds, archived included, since a
// restored row reads its files again.
func (m *Manager) PruneOrphans(live map[string]bool, now time.Time) (int, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	var errs []error
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		id, _, found := strings.Cut(entry.Name(), ".")
		if !found || !isSessionID(id) || live[id] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < OrphanGrace {
			continue
		}
		if err := removeIfExists(filepath.Join(m.dir, entry.Name())); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// isSessionID reports whether name has the shape the board gives a session
// id: four random bytes in hex, or the longer timestamp fallback.
func isSessionID(name string) bool {
	if len(name) < 8 {
		return false
	}
	for _, r := range name {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
