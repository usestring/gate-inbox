package tmux

import (
	"strings"
	"testing"
)

// The startup pass has to leave every session looking exactly as the
// per-session calls left it -- the label on the status bar, the window named
// after it, the hints on the right -- while costing a fixed few processes
// rather than seven per session.
func TestRefreshChromeBatchMatchesThePerSessionCalls(t *testing.T) {
	driver := requireTmux(t)
	var entries []ChromeEntry
	for i := 0; i < 4; i++ {
		id := uniqueID("chrome")
		if err := driver.Create(id, "/tmp", "", nil, 0, 0); err != nil {
			t.Fatalf("Create: %v", err)
		}
		t.Cleanup(func() { driver.Kill(id) })
		entries = append(entries, ChromeEntry{ID: id, Label: "batched-" + id})
	}

	// One session styled the old way, to compare the batch against -- and to
	// count what that way costs, which is the whole reason for the new one.
	solo := entries[0]
	perSession := countingTmux(t, driver)
	soloBefore := len(perSession())
	if err := driver.RefreshChrome(solo.ID); err != nil {
		t.Fatalf("RefreshChrome: %v", err)
	}
	if err := driver.SetLabel(solo.ID, solo.Label); err != nil {
		t.Fatalf("SetLabel: %v", err)
	}
	soloForks := len(perSession()) - soloBefore
	wantRight := option(t, solo.ID, "#{T:status-right}")
	wantLeft := option(t, solo.ID, "#{T:status-left}")

	calls := countingTmux(t, driver)
	before := len(calls())
	if err := driver.RefreshChromeBatch(entries); err != nil {
		t.Fatalf("RefreshChromeBatch: %v", err)
	}
	forked := calls()[before:]

	for _, entry := range entries {
		if got := option(t, entry.ID, "#{T:status-left}"); !strings.Contains(got, entry.Label) {
			t.Errorf("%s: status-left is %q, want it to carry %q", entry.ID, got, entry.Label)
		}
		if got := option(t, entry.ID, "#{window_name}"); got != entry.Label {
			t.Errorf("%s: window name is %q, want %q", entry.ID, got, entry.Label)
		}
		if got := option(t, entry.ID, "#{T:status-right}"); got != wantRight {
			t.Errorf("%s: status-right is %q, want the per-session call's %q", entry.ID, got, wantRight)
		}
		if got := option(t, entry.ID, "#{?status,on,off}"); got != "on" {
			t.Errorf("%s: status bar is %q", entry.ID, got)
		}
	}
	if !strings.Contains(wantLeft, solo.Label) {
		t.Fatalf("the per-session call left status-left as %q; the comparison is broken", wantLeft)
	}

	// Three processes: the listing, the prefix read, the write. The point of
	// the pass is that this number does not grow with the board.
	if len(forked) != 3 {
		t.Fatalf("the batch forked %d processes for %d sessions, want 3: %q", len(forked), len(entries), forked)
	}
	t.Logf("%d sessions: %d processes batched, against %d for one session the per-session way",
		len(entries), len(forked), soloForks)
}

// A pane the manager did not create keeps its own status bar, and a session
// that is not running is not an error -- the pass must not stop on either.
func TestRefreshChromeBatchSkipsAdoptedAndDeadSessions(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	adoptedID := uniqueID("adoptedchrome")
	adopt(t, driver, adoptedID, socket, panes[0])

	liveID := uniqueID("livechrome")
	if err := driver.Create(liveID, "/tmp", "", nil, 0, 0); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(liveID) })

	before := option(t, adoptedID, "#{T:status-left}")
	err := driver.RefreshChromeBatch([]ChromeEntry{
		{ID: adoptedID, Label: "not-yours"},
		{ID: uniqueID("neverexisted"), Label: "gone"},
		{ID: liveID, Label: "mine"},
	})
	if err != nil {
		t.Fatalf("RefreshChromeBatch: %v", err)
	}
	if got := option(t, adoptedID, "#{T:status-left}"); got != before {
		t.Errorf("the adopted pane's status bar changed to %q, was %q", got, before)
	}
	if got := option(t, liveID, "#{window_name}"); got != "mine" {
		t.Errorf("the live session was skipped along with them: window name %q", got)
	}
}

// option reads one format off a session, which is how tmux answers what it
// actually holds rather than what was written at it.
func option(t *testing.T, id, format string) string {
	t.Helper()
	out, err := tmuxCmd("display-message", "-p", "-t", "gi_"+id, format).CombinedOutput()
	if err != nil {
		t.Fatalf("display-message %s on %s: %v: %s", format, id, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A batch whose labels carry it past what one tmux invocation takes still
// lands, on every session in it. Before the split this was one list: tmux
// refused the whole thing with "command too long" and styled nobody, and the
// pass fell back to eleven forks a session for all thirty-two of them.
func TestRefreshChromeBatchSplitsAListTooLongForTmux(t *testing.T) {
	driver := requireTmux(t)
	// Long enough that these cannot share one list, and past tmux's own
	// ceiling too: each session carries its label twice, in status-left and in
	// the window name, so eight of them is about 26 KB of arguments.
	label := strings.Repeat("z", 1600)
	var entries []ChromeEntry
	for i := 0; i < 8; i++ {
		id := uniqueID("chromesplit")
		if err := driver.Create(id, "/tmp", "", nil, 0, 0); err != nil {
			t.Fatalf("Create: %v", err)
		}
		t.Cleanup(func() { driver.Kill(id) })
		entries = append(entries, ChromeEntry{ID: id, Label: label})
	}

	if err := driver.RefreshChromeBatch(entries); err != nil {
		t.Fatalf("RefreshChromeBatch: %v", err)
	}
	for _, entry := range entries {
		if got := option(t, entry.ID, "#{T:status-left}"); !strings.Contains(got, label) {
			t.Fatalf("session %s kept status-left %q, want the label the batch set", entry.ID, truncate(got))
		}
	}
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
