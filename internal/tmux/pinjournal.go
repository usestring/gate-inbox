package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/usestring/gate-inbox/internal/logging"
)

// The pin journal is what makes a pin survivable.
//
// Resizing a window forces its window-size to "manual", and only the manager
// knows what the window had before. Every path out of the process restores
// them -- quit, release, SIGHUP -- but SIGKILL is out of reach by
// construction, and so is a power cut. What that used to leave behind was a
// window frozen at the width of a preview panel belonging to a process that
// no longer exists, on a server whose windows mostly belong to the operator.
// There was no way to get it back except to know the tmux incantation.
//
// So the pins are written down. The next manager to start reads the journal
// and hands back anything the last one could not, before it pins anything of
// its own. That is a heal on the next run rather than at the moment of death,
// which is the best a killed process can offer -- and it is the difference
// between a stuck window and a permanently stuck window.
//
// The file is small by design: with pins held only for the window actually
// being previewed, it holds one entry, occasionally two for a split.

// journalEntry is one pinned window as it survives a crash: where to reach it
// and what to put back.
type journalEntry struct {
	Socket string `json:"socket"`
	Window string `json:"window"`
	// Prior is the window-size the window had. Empty means it was inheriting
	// the server's setting and has to go back to inheriting it, which is a
	// different act from being pinned to whatever that setting says today --
	// see restoreWindow.
	Prior string `json:"prior"`
}

// SetPinJournal points the driver at the file its pins are written to. A
// driver with no journal still pins and restores normally; it just has
// nothing to offer the run after a kill.
func (d *Driver) SetPinJournal(path string) {
	d.pinsMu.Lock()
	d.journalPath = path
	d.pinsMu.Unlock()
	d.writeJournal()
}

// writeJournal rewrites the file from the pins currently held.
//
// A rewrite rather than an append: the file is the live set, not a history,
// and a reader that had to replay adds against removes could be defeated by a
// write that landed half-finished. It is written whole to a temporary file
// and renamed, so a crash mid-write leaves either the old set or the new one
// and never a truncated file that would strand every pin in it.
func (d *Driver) writeJournal() {
	d.pinsMu.Lock()
	path := d.journalPath
	entries := make([]journalEntry, 0, len(d.pins))
	for _, pin := range d.pins {
		entries = append(entries, journalEntry{Socket: pin.socket, Window: pin.window, Prior: pin.prior})
	}
	d.pinsMu.Unlock()
	if path == "" {
		return
	}
	// Sorted so an unchanged set writes identical bytes, which makes the file
	// readable by a human trying to work out what the manager is holding.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Socket != entries[j].Socket {
			return entries[i].Socket < entries[j].Socket
		}
		return entries[i].Window < entries[j].Window
	})
	if len(entries) == 0 {
		// Nothing held: the file's continued existence would strand the next
		// run in a restore of windows already handed back.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			logging.Warn("pin journal remove", "path", path, logging.Err(err))
		}
		return
	}
	blob, err := json.Marshal(entries)
	if err != nil {
		logging.Warn("pin journal encode", logging.Err(err))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logging.Warn("pin journal dir", "path", path, logging.Err(err))
		return
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, blob, 0o644); err != nil {
		logging.Warn("pin journal write", "path", temp, logging.Err(err))
		return
	}
	if err := os.Rename(temp, path); err != nil {
		logging.Warn("pin journal rename", "path", path, logging.Err(err))
		os.Remove(temp)
	}
}

// RestoreJournaledPins hands back every window a previous run pinned and did
// not live to restore, then clears the journal.
//
// It runs before this manager pins anything, so a window it is about to pin
// itself is first returned to what the operator had -- otherwise the pin
// recorded now would be the dead manager's "manual" rather than the value
// underneath it, and the setting would be lost for good.
//
// A window that has since closed, or a server that has since exited, is
// nothing to hand back: restoreWindow's failures are already logged and are
// not this function's to report.
func (d *Driver) RestoreJournaledPins(path string) {
	blob, err := os.ReadFile(path)
	if err != nil {
		// No journal is the normal case: the last run exited cleanly.
		if !os.IsNotExist(err) {
			logging.Warn("pin journal read", "path", path, logging.Err(err))
		}
		return
	}
	var entries []journalEntry
	if err := json.Unmarshal(blob, &entries); err != nil {
		// A file this run cannot read is a file it cannot act on, and
		// keeping it would repeat the complaint on every start.
		logging.Warn("pin journal decode", "path", path, logging.Err(err))
		os.Remove(path)
		return
	}
	for _, entry := range entries {
		if entry.Socket == "" || entry.Window == "" {
			continue
		}
		logging.Info("restoring a window a killed manager left pinned",
			"socket", entry.Socket, "window", entry.Window, "window-size", entry.Prior)
		d.restoreWindow(&windowPin{socket: entry.Socket, window: entry.Window, prior: entry.Prior})
	}
	os.Remove(path)
}
