package agentsession

import (
	"time"

	"github.com/usestring/gate-inbox/internal/convo"
)

// omp (oh-my-pi) mints its own conversation id: it has no --session-id, and
// writes <timestamp>_<id>.jsonl into its sessions directory only once the
// first message goes out, with a header naming the id, the directory and
// when the conversation started.

// captureOmp is the conversation omp started in cwd at or after launchedAt
// that no other session holds. Two sessions launched into one directory
// within the clock slack of each other cannot be told apart from the files,
// so more than one candidate refuses rather than guesses; each is picked up
// once its rival is claimed.
func captureOmp(store convo.PiStore, cwd string, launchedAt time.Time, claimed map[string]bool) (string, bool) {
	cutoff := launchedAt.Add(-clockSlack)
	var found []string
	for _, s := range store.SessionsFor(cwd, cutoff) {
		if claimed[s.Header.ID] {
			continue
		}
		started, err := time.Parse(time.RFC3339Nano, s.Header.Timestamp)
		if err != nil || started.Before(cutoff) {
			// An older conversation written to since: a resume, not ours.
			continue
		}
		found = append(found, s.Header.ID)
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// snapshotOmp is Snapshot over omp's store: every conversation it keeps for
// cwd with its file's modification time.
func snapshotOmp(store convo.PiStore, cwd string) (map[string]int64, bool) {
	if store.Root == "" {
		return nil, false
	}
	snapshot := map[string]int64{}
	for _, s := range store.SessionsFor(cwd, time.Time{}) {
		snapshot[s.Header.ID] = s.ModTime.UnixNano()
	}
	return snapshot, true
}

// recaptureOmp lists the unclaimed omp conversations for cwd whose files
// moved past the snapshot.
func recaptureOmp(store convo.PiStore, cwd string, snapshot map[string]int64, claimed map[string]bool) []string {
	var matched []string
	for _, s := range store.SessionsFor(cwd, time.Time{}) {
		if !claimed[s.Header.ID] && afterSnapshot(snapshot, s.Header.ID, s.ModTime.UnixNano()) {
			matched = append(matched, s.Header.ID)
		}
	}
	return matched
}
