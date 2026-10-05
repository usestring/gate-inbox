package tmux

import (
	"slices"
	"strings"
	"testing"
)

// strayWindow opens a window in managed session id the way a `tmux
// new-window` typed in one of its panes does, and returns its pane. It runs
// cat so the pane stays up until something ends it.
func strayWindow(t *testing.T, driver *Driver, id string) string {
	t.Helper()
	out, err := tmuxOn(driver.socket, "new-window", "-d", "-t", "="+SessionName(id), "-P", "-F", "#{pane_id}", "cat").CombinedOutput()
	if err != nil {
		t.Fatalf("new-window in %s: %v: %s", id, err, out)
	}
	return strings.TrimSpace(string(out))
}

func createManaged(t *testing.T, driver *Driver, prefix, command string) string {
	t.Helper()
	id := uniqueID(prefix)
	if err := driver.Create(id, "/tmp", command, nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		driver.Kill(id)
		tmuxOn(driver.socket, "kill-session", "-t", "="+leftoverName(id)).Run()
	})
	return id
}

func paneAlive(t *testing.T, driver *Driver, pane string) bool {
	t.Helper()
	return slices.Contains(livePanes(t, driver.socket), pane)
}

func leftoverExists(driver *Driver, id string) bool {
	return tmuxOn(driver.socket, "has-session", "-t", "="+leftoverName(id)).Run() == nil
}

// A window somebody opened inside a managed session is theirs. Archiving one
// terminal row took eleven adopted agents down with it because Kill ended the
// whole gi_ session they had been launched into.
func TestKillLeavesAWindowSomebodyElseOpened(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"agent", "cat"},
		// A terminal row runs no launch script, so its home is the oldest window.
		{"terminal", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := requireTmux(t)
			id := createManaged(t, driver, "killscope"+tc.name, tc.command)
			own, err := driver.PaneID(id)
			if err != nil {
				t.Fatalf("PaneID: %v", err)
			}
			stray := strayWindow(t, driver, id)
			adopted := uniqueID("strayadopted")
			adopt(t, driver, adopted, driver.socket, stray)
			t.Cleanup(func() { driver.Release(adopted) })

			if err := driver.Kill(id); err != nil {
				t.Fatalf("Kill: %v", err)
			}
			waitGone(t, driver, id)
			if paneAlive(t, driver, own) {
				t.Fatalf("Kill left the session's own pane %s running", own)
			}
			if !paneAlive(t, driver, stray) {
				t.Fatalf("Kill ended %s, a window the manager never started", stray)
			}
			if !driver.Exists(adopted) {
				t.Fatal("the adopted pane in the stray window should still be alive")
			}
			if !leftoverExists(driver, id) {
				t.Fatalf("the stray window should be left in session %s", leftoverName(id))
			}
		})
	}
}

// A session holding nothing but its own window still goes whole.
func TestKillEndsASessionWithOnlyItsOwnWindow(t *testing.T) {
	driver := requireTmux(t)
	id := createManaged(t, driver, "killwhole", "cat")
	if err := driver.Kill(id); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitGone(t, driver, id)
	if leftoverExists(driver, id) {
		t.Fatal("a session with no stray windows should not leave a renamed one behind")
	}
}

// Once the agent's own window has closed, every window left is somebody
// else's, and Kill ends none of them.
func TestKillWithTheAgentWindowGoneEndsNothingElse(t *testing.T) {
	driver := requireTmux(t)
	id := createManaged(t, driver, "killhomegone", "cat")
	own, err := driver.PaneID(id)
	if err != nil {
		t.Fatalf("PaneID: %v", err)
	}
	stray := strayWindow(t, driver, id)
	if out, err := tmuxOn(driver.socket, "kill-pane", "-t", own).CombinedOutput(); err != nil {
		t.Fatalf("kill-pane %s: %v: %s", own, err, out)
	}

	if err := driver.Kill(id); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitGone(t, driver, id)
	if !paneAlive(t, driver, stray) {
		t.Fatalf("Kill ended %s though the agent's own window was already gone", stray)
	}
}

// KillPane ends the session's own window from a pane read off it, and a pane
// read off a stray window ends nothing.
func TestKillPaneTakesOnlyTheOwnWindow(t *testing.T) {
	driver := requireTmux(t)
	id := createManaged(t, driver, "killpanescope", "cat")
	own, err := driver.PaneID(id)
	if err != nil {
		t.Fatalf("PaneID: %v", err)
	}
	stray := strayWindow(t, driver, id)

	if err := driver.KillPane(id, stray); err == nil {
		t.Fatal("KillPane with a stray window's pane should refuse")
	}
	if !paneAlive(t, driver, own) || !paneAlive(t, driver, stray) {
		t.Fatal("a refused KillPane must end nothing")
	}

	if err := driver.KillPane(id, own); err != nil {
		t.Fatalf("KillPane: %v", err)
	}
	if paneAlive(t, driver, own) {
		t.Fatalf("KillPane left %s running", own)
	}
	if !paneAlive(t, driver, stray) {
		t.Fatalf("KillPane ended %s, a window the manager never started", stray)
	}
}

// With nothing else in the session, KillPane still ends it whole.
func TestKillPaneEndsASessionWithOnlyItsOwnWindow(t *testing.T) {
	driver := requireTmux(t)
	id := createManaged(t, driver, "killpanewhole", "cat")
	own, err := driver.PaneID(id)
	if err != nil {
		t.Fatalf("PaneID: %v", err)
	}
	if err := driver.KillPane(id, own); err != nil {
		t.Fatalf("KillPane: %v", err)
	}
	waitGone(t, driver, id)
	if leftoverExists(driver, id) {
		t.Fatal("a session with no stray windows should not leave a renamed one behind")
	}
}

// The home is the window the session was created with, not whatever its pane
// now runs: a respawn replaces the launch script, and the stop path and the
// session created before the record both still find their own window.
func TestKillFindsTheOwnWindowAfterARespawnAndWithoutTheRecord(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, driver *Driver, id, own string)
	}{
		{"respawned", func(t *testing.T, driver *Driver, id, own string) {
			if out, err := tmuxOn(driver.socket, "respawn-pane", "-k", "-t", own, "cat").CombinedOutput(); err != nil {
				t.Fatalf("respawn-pane: %v: %s", err, out)
			}
		}},
		{"unrecorded", func(t *testing.T, driver *Driver, id, own string) {
			if out, err := tmuxOn(driver.socket, "set-option", "-u", "-t", "="+SessionName(id)+":", homeOption).CombinedOutput(); err != nil {
				t.Fatalf("set-option -u: %v: %s", err, out)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := requireTmux(t)
			id := createManaged(t, driver, "killhome"+tc.name, "cat")
			own, err := driver.PaneID(id)
			if err != nil {
				t.Fatalf("PaneID: %v", err)
			}
			stray := strayWindow(t, driver, id)
			tc.mutate(t, driver, id, own)

			if err := driver.Kill(id); err != nil {
				t.Fatalf("Kill: %v", err)
			}
			waitGone(t, driver, id)
			if paneAlive(t, driver, own) {
				t.Fatalf("Kill left the session's own pane %s running", own)
			}
			if !paneAlive(t, driver, stray) {
				t.Fatalf("Kill ended %s, a window the manager never started", stray)
			}
		})
	}
}
