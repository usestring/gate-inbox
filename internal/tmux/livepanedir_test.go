package tmux

import "testing"

// LivePaneDir answers Exists and PaneCurrentPath together, so it has to give
// Exists's answer in the case Exists works hardest for: an adopted pane that
// has closed, which display-message answers with an empty line and exit 0.
func TestLivePaneDirAgreesWithExists(t *testing.T) {
	driver := requireTmux(t)
	managed := uniqueID("dir")
	if err := driver.Create(managed, "/tmp", "cat", nil, 80, 24); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { driver.Kill(managed) })
	socket, panes := foreignServer(t)
	open, closed := uniqueID("open"), uniqueID("closed")
	adopt(t, driver, open, socket, panes[1])
	adopt(t, driver, closed, socket, panes[0])
	if out, err := tmuxOn(socket, "kill-pane", "-t", panes[0]).CombinedOutput(); err != nil {
		t.Fatalf("kill-pane: %v: %s", err, out)
	}
	for _, c := range []struct {
		id, dir string
		live    bool
	}{
		{managed, "/tmp", true},
		{open, "/tmp", true},
		{closed, "", false},
		{uniqueID("never"), "", false},
	} {
		dir, live := driver.LivePaneDir(c.id)
		if live != c.live || dir != c.dir {
			t.Errorf("LivePaneDir(%s) = %q, %v; want %q, %v", c.id, dir, live, c.dir, c.live)
		}
		if exists := driver.Exists(c.id); exists != live {
			t.Errorf("LivePaneDir(%s) live = %v, Exists = %v", c.id, live, exists)
		}
	}
}
