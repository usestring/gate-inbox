package accounts

import (
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
)

// listingFresh is how long a cached accounts_command output is served
// without starting a refresh. A secret store's listing is a network call
// that takes seconds (gcloud secrets list is about two), and the set of
// accounts changes on the scale of days, so the board answers from the last
// listing and refreshes it behind the card.
const listingFresh = 30 * time.Second

// listings caches each accounts_command's output by command line. Only the
// raw output is kept: List's parsing, unavailability marks included, runs
// on every call, so a mark still takes an account off the card at once.
var listings = struct {
	sync.Mutex
	byCommand map[string]*listing
}{byCommand: map[string]*listing{}}

type listing struct {
	out string
	at  time.Time
	// done is open while a read is under way and closed when it ends, so a
	// caller with nothing cached waits on that read rather than starting
	// another.
	done chan struct{}
	err  error
	ok   bool
}

// ListCached is List for the board, where it runs on the event loop: it
// answers from the last successful listing of the same command, starting a
// background refresh once that listing is older than listingFresh. Only a
// command never listed (or whose last listing failed) is read while the
// caller waits, and Prefetch exists so the board starts that read before
// anyone presses a key.
func ListCached(tool config.Tool) ([]string, error) {
	return list(tool, cachedRun)
}

// Prefetch starts reading the tool's accounts_command in the background
// unless a fresh listing is already cached, so a later ListCached answers
// at once.
func Prefetch(tool config.Tool) {
	if tool.AccountsCommand == "" {
		return
	}
	listings.Lock()
	defer listings.Unlock()
	entry := listings.byCommand[tool.AccountsCommand]
	if entry != nil && (entry.done != nil || entry.ok && time.Since(entry.at) < listingFresh) {
		return
	}
	startRefresh(tool.AccountsCommand, entry)
}

func cachedRun(command string) (string, error) {
	listings.Lock()
	entry := listings.byCommand[command]
	switch {
	case entry != nil && entry.ok:
		if entry.done == nil && time.Since(entry.at) >= listingFresh {
			startRefresh(command, entry)
		}
		out := entry.out
		listings.Unlock()
		return out, nil
	case entry == nil || entry.done == nil:
		entry = startRefresh(command, entry)
	}
	done := entry.done
	listings.Unlock()
	<-done
	listings.Lock()
	defer listings.Unlock()
	if entry.ok {
		return entry.out, nil
	}
	return "", entry.err
}

// startRefresh reads command in the background into entry, creating it when
// nil. listings must be held. A failed read keeps the last good output, so
// one timeout does not empty a card that was listing fine.
func startRefresh(command string, entry *listing) *listing {
	if entry == nil {
		entry = &listing{}
		listings.byCommand[command] = entry
	}
	done := make(chan struct{})
	entry.done = done
	read := run
	go func() {
		out, err := read(command)
		listings.Lock()
		defer listings.Unlock()
		if err == nil {
			entry.out, entry.at, entry.ok, entry.err = out, time.Now(), true, nil
		} else {
			entry.err = err
		}
		entry.done = nil
		close(done)
	}()
	return entry
}
