package accounts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
)

// listingFresh is how long a cached accounts_command output is served
// without starting a refresh. A secret store's listing is a network call
// that takes seconds (gcloud secrets list is about two), and the set of
// accounts changes on the scale of days, so callers answer from the last
// listing and it is refreshed behind them.
const listingFresh = 30 * time.Second

// listingFile is the one listing every process shares: the board, each
// session's MCP server and the CLI read it, and one refresh serves them
// all. Keyed by command line, so two CLIs listing the same secret store
// share an entry.
const listingFile = "account_listings.json"

// listings is this process's copy of the shared file, plus the refreshes it
// has in flight. Only the raw output is kept: List's parsing, unavailability
// marks included, runs on every call, so a mark still takes an account off
// the card at once.
var listings = struct {
	sync.Mutex
	byCommand map[string]*listing
	// dir holds listingFile; "" is the config directory.
	dir string
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

type storedListing struct {
	Out string    `json:"out"`
	At  time.Time `json:"at"`
}

// ListCached is List for callers that cannot wait on a secret store: the
// board's event loop, and the account chooser behind it. It answers from
// the last successful listing of the same command, this process's or the
// shared file's, starting a background refresh once that is older than
// listingFresh. Only a command never listed on this machine (or whose every
// listing failed) is read while the caller waits, and Prefetch lets the
// board start that read before anyone presses a key.
func ListCached(tool config.Tool) ([]string, error) {
	return list(tool, cachedRun)
}

// Prefetch starts reading the tool's accounts_command in the background
// unless a fresh listing is already in hand, so a later ListCached answers
// at once.
func Prefetch(tool config.Tool) {
	if tool.AccountsCommand == "" {
		return
	}
	listings.Lock()
	defer listings.Unlock()
	entry := loadLocked(tool.AccountsCommand)
	if entry.done != nil || entry.ok && time.Since(entry.at) < listingFresh {
		return
	}
	startRefresh(tool.AccountsCommand, entry)
}

// UseListingDir moves the shared listing file to dir and drops this
// process's copy, so a test neither reads nor writes the operator's.
func UseListingDir(dir string) (restore func()) {
	listings.Lock()
	defer listings.Unlock()
	previous := listings.dir
	listings.dir, listings.byCommand = dir, map[string]*listing{}
	return func() {
		listings.Lock()
		defer listings.Unlock()
		listings.dir, listings.byCommand = previous, map[string]*listing{}
	}
}

func cachedRun(command string) (string, error) {
	listings.Lock()
	entry := loadLocked(command)
	if entry.ok {
		if entry.done == nil && time.Since(entry.at) >= listingFresh {
			startRefresh(command, entry)
		}
		out := entry.out
		listings.Unlock()
		return out, nil
	}
	if entry.done == nil {
		startRefresh(command, entry)
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

// loadLocked is this process's entry for command, seeded from the shared
// file the first time, so a board that has just restarted answers from the
// listing the last one, or any MCP server, left. listings must be held.
func loadLocked(command string) *listing {
	if entry, ok := listings.byCommand[command]; ok {
		return entry
	}
	entry := &listing{}
	if stored, ok := readListings(listings.dir)[command]; ok {
		entry.out, entry.at, entry.ok = stored.Out, stored.At, true
	}
	listings.byCommand[command] = entry
	return entry
}

// startRefresh brings entry up to date in the background. listings must be
// held. Under the file's lock it first takes a listing another process
// wrote since, and runs command only when there is none fresh, so the board
// and every MCP server refresh at most once between them. A failed read
// keeps the last good output, so one timeout does not empty a card that was
// listing fine.
func startRefresh(command string, entry *listing) {
	done := make(chan struct{})
	entry.done = done
	read, dir := run, listings.dir
	go func() {
		out, at, err := refreshShared(dir, command, read)
		listings.Lock()
		defer listings.Unlock()
		if err == nil {
			entry.out, entry.at, entry.ok, entry.err = out, at, true, nil
		} else {
			entry.err = err
		}
		entry.done = nil
		close(done)
	}()
}

func refreshShared(dir, command string, read func(string) (string, error)) (string, time.Time, error) {
	path := listingPath(dir)
	if path == "" {
		out, err := read(command)
		return out, time.Now(), err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		if unlock, err := lockFile(path + ".lock"); err == nil {
			defer unlock()
		}
	}
	stored := readListings(dir)
	if entry, ok := stored[command]; ok && time.Since(entry.At) < listingFresh {
		return entry.Out, entry.At, nil
	}
	out, err := read(command)
	if err != nil {
		return "", time.Time{}, err
	}
	at := time.Now()
	stored[command] = storedListing{Out: out, At: at}
	writeListings(path, stored)
	return out, at, nil
}

// listingPath is where the shared listing lives, or "" when there is no
// config directory to keep it in; then each process lists for itself.
func listingPath(dir string) string {
	if dir == "" {
		var err error
		if dir, err = config.Dir(); err != nil {
			return ""
		}
	}
	return filepath.Join(dir, listingFile)
}

// readListings is the shared file's listings; an absent or unreadable file
// is none, which only means the next caller lists for itself.
func readListings(dir string) map[string]storedListing {
	stored := map[string]storedListing{}
	if path := listingPath(dir); path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(raw, &stored)
		}
	}
	return stored
}

// writeListings replaces the file whole, through a rename, so a reader in
// another process sees the old listing or the new one and never half of
// one. A failed write is dropped: this process still has its own copy.
func writeListings(path string, stored map[string]storedListing) {
	raw, err := json.Marshal(stored)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), listingFile+".*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() == nil {
		_ = os.Rename(tmp.Name(), path)
	}
}
