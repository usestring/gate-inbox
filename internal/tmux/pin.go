package tmux

import (
	"strings"
	"sync"
)

// windowPin is one window Resize pinned: where to reach it, and what its
// window-size was before resize-window forced the window to "manual".
//
// holders refcounts by session because a split window can hold two adopted
// panes. The first pin records what to put back and only the last release
// puts it back, so releasing one pane neither unpins a window the other is
// still previewed in nor lets the second pin record the first one's "manual"
// as what the operator had.
type windowPin struct {
	socket string
	// window is tmux's own "@4" rather than the pane that named it. A pane
	// can be killed while its window lives on with the manager's pin still
	// on it, and a window id is never reused, so a restore aimed at one that
	// has closed cannot land on somebody else's window.
	window  string
	prior   string
	holders map[string]bool
}

// readWindowSize names the window a target sits in and reads that window's
// own window-size, in one invocation.
//
// An option that is not set at window scope prints nothing at all, which is
// the distinction the restore turns on: a value written back is a value the
// window then has locally, and a window that was inheriting the server's
// setting has to go back to inheriting it rather than be pinned to whatever
// that setting happened to say.
func (d *Driver) readWindowSize(socket, target string) (window, prior string, ok bool) {
	out, err := d.combined([]string{"-L", socket,
		"display-message", "-p", "-t", target, "#{window_id}", ";",
		"show-window-options", "-v", "-t", target, "window-size"})
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if window = strings.TrimSpace(lines[0]); window == "" {
		return "", "", false
	}
	if len(lines) > 1 {
		prior = strings.TrimSpace(lines[1])
	}
	return window, prior, true
}

// pinWindow records what a window's sizing was before Resize takes it over.
//
// A session already holding a pin is never re-read. PrepareAttach writes
// "latest" of its own on the way to an attach, and the detach that follows
// re-pins, so a second read would record the manager's own value as the one
// the operator had.
func (d *Driver) pinWindow(id, socket, target string) {
	d.pinsMu.Lock()
	_, held := d.pinBySession[id]
	d.pinsMu.Unlock()
	if held {
		return
	}
	// Read off the lock: a window resize pins every session at once, and a
	// fork per session under one mutex would serialize the whole board.
	window, prior, ok := d.readWindowSize(socket, target)
	if !ok {
		return
	}

	d.pinsMu.Lock()
	if _, held := d.pinBySession[id]; held {
		d.pinsMu.Unlock()
		return
	}
	if d.pins == nil {
		d.pins = map[string]*windowPin{}
		d.pinBySession = map[string]string{}
	}
	key := socket + "\x00" + window
	pin, ok := d.pins[key]
	if !ok {
		pin = &windowPin{socket: socket, window: window, prior: prior, holders: map[string]bool{}}
		d.pins[key] = pin
	}
	pin.holders[id] = true
	d.pinBySession[id] = key
	d.pinsMu.Unlock()
	// Recorded to disk as it is taken, not at exit: the journal exists for
	// the exit that never runs. It takes the same mutex, so it goes after
	// the unlock rather than under it.
	d.writeJournal()
}

// ReleaseSize hands one session's window back to the sizing it had, for a
// session the manager has stopped previewing.
//
// It is the counterpart to Resize and the reason a pin is now a temporary
// state rather than a permanent one. Resizing forces window-size to manual,
// and a window left that way is frozen at the width of a preview panel for
// as long as tmux keeps running -- on a board that is mostly the operator's
// own windows, which they are also using directly.
//
// Releasing a session that holds no pin is a no-op, so callers may release
// freely rather than tracking what they pinned.
func (d *Driver) ReleaseSize(id string) {
	d.unpinSession(id)
}

// unpinSession hands one session's window back, once no other session on the
// board is being previewed through that same window.
func (d *Driver) unpinSession(id string) {
	d.pinsMu.Lock()
	pin := d.dropPinLocked(id)
	d.pinsMu.Unlock()
	if pin != nil {
		d.restoreWindow(pin)
	}
	d.writeJournal()
}

// forgetPin drops a session's pin without restoring anything, for a session
// whose window is going away with it. It also keeps the id honest: a revived
// session is a new window under the same id, and a stale entry here would
// stop the new one from ever being recorded.
func (d *Driver) forgetPin(id string) {
	d.pinsMu.Lock()
	d.dropPinLocked(id)
	d.pinsMu.Unlock()
	d.writeJournal()
}

// PinnedSessions is every session whose window this manager currently holds
// pinned. The model releases against it rather than remembering what it
// pinned, so a pin taken on a path the model has forgotten about is still
// found and handed back.
func (d *Driver) PinnedSessions() []string {
	d.pinsMu.Lock()
	defer d.pinsMu.Unlock()
	ids := make([]string, 0, len(d.pinBySession))
	for id := range d.pinBySession {
		ids = append(ids, id)
	}
	return ids
}

// dropPinLocked removes a session from its window's holders and returns the
// pin when that was the last one, meaning the window is the caller's to
// restore.
func (d *Driver) dropPinLocked(id string) *windowPin {
	key, held := d.pinBySession[id]
	if !held {
		return nil
	}
	delete(d.pinBySession, id)
	pin, ok := d.pins[key]
	if !ok {
		return nil
	}
	delete(pin.holders, id)
	if len(pin.holders) > 0 {
		return nil
	}
	delete(d.pins, key)
	return pin
}

// RestorePinnedWindows hands every window the manager resized back to the
// sizing it had, and is what the manager's exit owes the operator: on a
// shared server those windows are theirs, and a pinned one stays frozen at
// the preview panel's size for as long as tmux keeps running -- long after
// the manager that pinned it is gone.
//
// Only windows this driver pinned are touched. An operator who set window-size
// manual themselves means it, and a manager that never resized their window
// has nothing to hand back.
func (d *Driver) RestorePinnedWindows() {
	d.pinsMu.Lock()
	pins := d.pins
	d.pins = nil
	d.pinBySession = nil
	d.pinsMu.Unlock()
	d.writeJournal()

	var wg sync.WaitGroup
	for _, pin := range pins {
		wg.Add(1)
		go func(pin *windowPin) {
			defer wg.Done()
			d.restoreWindow(pin)
		}(pin)
	}
	wg.Wait()
}

// restoreWindow puts back what the window actually had rather than a value
// the manager considers normal: an assumed default is how a manager comes to
// overwrite a setting it was never asked about.
//
// A window that was inheriting the server's window-size is unset rather than
// written, so a later change to that server setting still reaches it.
func (d *Driver) restoreWindow(pin *windowPin) {
	args := []string{"-L", pin.socket, "set-window-option", "-t", pin.window, "window-size", pin.prior}
	if pin.prior == "" {
		args = []string{"-L", pin.socket, "set-window-option", "-u", "-t", pin.window, "window-size"}
	}
	// A window closed since it was pinned is nothing to hand back, and
	// combined has already logged whatever tmux said.
	d.combined(args)
}
