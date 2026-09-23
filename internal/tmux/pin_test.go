// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package tmux

import (
	"os"
	"testing"
)

// setForeignWindowSize is the operator setting window-size on their own
// window, which is the one case the manager must leave exactly as it found
// it.
func setForeignWindowSize(t *testing.T, socket, pane, value string) {
	t.Helper()
	if out, err := tmuxOn(socket, "set-window-option", "-t", pane, "window-size", value).CombinedOutput(); err != nil {
		t.Fatalf("set window-size %s: %v: %s", value, err, out)
	}
}

// The manager's exit is the last chance to hand a pinned window back. Left
// manual, the operator's own window stays frozen at the size of a preview
// panel that no longer exists: resizing their terminal moves nothing, and no
// later run of anything puts it right.
func TestExitUnpinsAWindowTheManagerResized(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("unpinexit")
	adopt(t, driver, id, socket, panes[0])

	// The window inherits the server's window-size, which is what a window
	// nobody has configured by hand looks like.
	if got := foreignWindowSize(t, socket, panes[0]); got != "" {
		t.Fatalf("the fresh window already had a window-size of its own: %q", got)
	}
	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got := foreignWindowSize(t, socket, panes[0]); got != "manual" {
		t.Fatalf("after Resize, window-size = %q, want manual", got)
	}

	driver.RestorePinnedWindows()

	if got := foreignWindowSize(t, socket, panes[0]); got != "" {
		t.Fatalf("after exit, window-size = %q, want it inheriting the server's again", got)
	}
}

// What goes back is what was there. A window that had a setting of its own
// keeps it: assuming the value the manager considers normal is how a manager
// comes to overwrite a setting nobody asked it about.
func TestExitRestoresTheWindowsOwnSetting(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("unpinown")
	setForeignWindowSize(t, socket, panes[0], "largest")
	adopt(t, driver, id, socket, panes[0])

	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	driver.RestorePinnedWindows()

	if got := foreignWindowSize(t, socket, panes[0]); got != "largest" {
		t.Fatalf("after exit, window-size = %q, want the largest the window had", got)
	}
}

// An operator who set window-size manual themselves meant it, and a window
// the manager never resized is not the manager's to change.
func TestExitLeavesAWindowItNeverResizedAlone(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("unpinother")
	setForeignWindowSize(t, socket, panes[0], "manual")
	adopt(t, driver, id, socket, panes[0])

	driver.RestorePinnedWindows()

	if got := foreignWindowSize(t, socket, panes[0]); got != "manual" {
		t.Fatalf("window-size = %q, want the manual the operator set", got)
	}
}

// A row is dropped while the manager keeps running -- pruned by the poller
// when its pane goes, or deleted by hand -- and the pane it was previewing
// has no reason to stay pinned to a panel it is no longer painted into.
func TestReleaseUnpinsTheWindow(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("unpinrelease")
	adopt(t, driver, id, socket, panes[0])

	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got := foreignWindowSize(t, socket, panes[0]); got != "manual" {
		t.Fatalf("after Resize, window-size = %q, want manual", got)
	}

	driver.Release(id)

	if got := foreignWindowSize(t, socket, panes[0]); got != "" {
		t.Fatalf("after Release, window-size = %q, want it inheriting the server's again", got)
	}
}

// A split window holds two panes, and the board can be previewing both. The
// pin belongs to the window rather than to either pane, so releasing one
// while the other is still on the board must leave it in place -- and the
// value the second release puts back is still the operator's, not the
// manual the first pin wrote.
func TestReleaseKeepsAWindowItsOtherPaneIsStillPinning(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	first, second := uniqueID("pinboth1"), uniqueID("pinboth2")
	adopt(t, driver, first, socket, panes[0])
	adopt(t, driver, second, socket, panes[1])

	for _, id := range []string{first, second} {
		if err := driver.Resize(id, 132, 60); err != nil {
			t.Fatalf("Resize %s: %v", id, err)
		}
	}

	driver.Release(first)
	if got := foreignWindowSize(t, socket, panes[1]); got != "manual" {
		t.Fatalf("releasing one pane unpinned a window the other is still previewed in: window-size = %q", got)
	}

	driver.Release(second)
	if got := foreignWindowSize(t, socket, panes[1]); got != "" {
		t.Fatalf("after the last release, window-size = %q, want it inheriting the server's again", got)
	}
}

// PrepareAttach writes a window-size of the manager's own on the way to an
// attach, and the detach that follows re-pins. Reading the window again at
// that point would record "latest" as what the operator had and hand them
// that instead of the setting they came with.
func TestExitRestoresThroughAnAttachAndDetach(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("pinattach")
	adopt(t, driver, id, socket, panes[0])

	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if err := driver.PrepareAttach(id); err != nil {
		t.Fatalf("PrepareAttach: %v", err)
	}
	// The detach side of an attach: the preview wants the pane back at the
	// panel's size.
	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize after detach: %v", err)
	}

	driver.RestorePinnedWindows()

	if got := foreignWindowSize(t, socket, panes[0]); got != "" {
		t.Fatalf("after exit, window-size = %q, want it inheriting the server's again", got)
	}
}

// A managed session's window is pinned by the same call, so it is handed
// back by the same one. Its server is the manager's own only when the
// manager was given a socket of its own; by default it is the operator's.
func TestExitUnpinsAManagedWindow(t *testing.T) {
	driver := requireTmux(t)
	id := uniqueID("pinmanaged")
	if err := driver.Create(id, t.TempDir(), "", nil, 100, 30); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(id) })

	if err := driver.Resize(id, 80, 24); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got := windowSizeOption(t, id); got != "manual" {
		t.Fatalf("after Resize, window-size = %q, want manual", got)
	}

	driver.RestorePinnedWindows()

	if got := windowSizeOption(t, id); got != "" {
		t.Fatalf("after exit, window-size = %q, want it inheriting the server's again", got)
	}
}

// A pin is temporary now, so the release has to work on its own rather than
// only as part of exit. Releasing while the manager is still running is the
// whole of the fix: the operator's windows are theirs again the moment the
// manager stops previewing them, not when it quits.
func TestReleaseSizeHandsTheWindowBackWhileRunning(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("release")
	adopt(t, driver, id, socket, panes[0])

	if got := foreignWindowSize(t, socket, panes[0]); got != "" {
		t.Fatalf("the fresh window already had a window-size of its own: %q", got)
	}
	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got := foreignWindowSize(t, socket, panes[0]); got != "manual" {
		t.Fatalf("while previewed, window-size = %q, want manual", got)
	}

	driver.ReleaseSize(id)

	if got := foreignWindowSize(t, socket, panes[0]); got != "" {
		t.Fatalf("after release, window-size = %q, want it inheriting the server's again", got)
	}
	if held := driver.PinnedSessions(); len(held) != 0 {
		t.Fatalf("the driver still reports %v pinned after releasing them", held)
	}
	// Releasing something that holds no pin is what lets a caller release
	// freely instead of tracking what it took.
	driver.ReleaseSize(id)
	driver.ReleaseSize("never-pinned")
}

// A release restores what the window had, exactly as exit does. The value is
// read once, when the pin is taken, so a release must not assume the default.
func TestReleaseSizeRestoresTheWindowsOwnSetting(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("releaseown")
	setForeignWindowSize(t, socket, panes[0], "largest")
	adopt(t, driver, id, socket, panes[0])

	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	driver.ReleaseSize(id)

	if got := foreignWindowSize(t, socket, panes[0]); got != "largest" {
		t.Fatalf("after release, window-size = %q, want the operator's own \"largest\" back", got)
	}
}

// The journal is what a manager that is killed outright leaves behind for the
// next one. Nothing else can help there: SIGKILL runs no defer, and until now
// the window stayed frozen for as long as tmux kept running.
func TestAKilledManagersPinIsRestoredByTheNextRun(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("journal")
	setForeignWindowSize(t, socket, panes[0], "largest")
	adopt(t, driver, id, socket, panes[0])

	journal := t.TempDir() + "/pins.json"
	driver.SetPinJournal(journal)
	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if got := foreignWindowSize(t, socket, panes[0]); got != "manual" {
		t.Fatalf("while previewed, window-size = %q, want manual", got)
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatalf("the pin was taken but nothing was written down: %v", err)
	}

	// The manager dies here. No restore runs, no defer fires, and the driver
	// that knew about the pin is simply gone -- so the next run gets a fresh
	// one, exactly as a new process would.
	next := requireTmux(t)
	next.RestoreJournaledPins(journal)

	if got := foreignWindowSize(t, socket, panes[0]); got != "largest" {
		t.Fatalf("after the next run started, window-size = %q, want the operator's own \"largest\" back", got)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("the journal survived the restore, so the run after this one would restore it again: %v", err)
	}
}

// A clean release clears the journal too. Left behind, it would have the next
// run restoring windows that were already handed back -- writing a stale
// window-size over whatever the operator had set since.
func TestACleanReleaseLeavesNothingForTheNextRunToDo(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("journalclean")
	adopt(t, driver, id, socket, panes[0])

	journal := t.TempDir() + "/pins.json"
	driver.SetPinJournal(journal)
	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	driver.ReleaseSize(id)

	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("the journal outlived the pin it recorded: %v", err)
	}
}
