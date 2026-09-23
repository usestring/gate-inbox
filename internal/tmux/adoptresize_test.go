package tmux

import (
	"strconv"
	"strings"
	"testing"
)

// paneSize is the adopted pane's own dimensions, read off the server that
// holds it rather than off the manager's.
func paneSize(t *testing.T, socket, pane string) (int, int) {
	t.Helper()
	raw := paneField(t, socket, pane, "#{pane_width}x#{pane_height}")
	w, h, ok := strings.Cut(raw, "x")
	if !ok {
		t.Fatalf("pane size %q for %s is not WIDTHxHEIGHT", raw, pane)
	}
	width, err := strconv.Atoi(w)
	if err != nil {
		t.Fatalf("pane width %q: %v", w, err)
	}
	height, err := strconv.Atoi(h)
	if err != nil {
		t.Fatalf("pane height %q: %v", h, err)
	}
	return width, height
}

func foreignWindowSize(t *testing.T, socket, pane string) string {
	t.Helper()
	out, err := tmuxOn(socket, "show-window-options", "-v", "-t", pane, "window-size").CombinedOutput()
	if err != nil {
		t.Fatalf("foreign show-window-options: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// The board is mostly panes the manager adopted, and a preview panel taller
// than the pane behind it paints a band of output with dead rows under it.
// Sizing an adopted window is aimed at the server that holds it: the manager's
// own socket has no gi_<id> to resize, and a resize that lands nowhere is the
// same dead panel with no error to show for it.
func TestAdoptedPaneSizesToThePreviewPanel(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("resize")
	// The first pane: the split leaves it the shorter of the two, which is
	// what a preview panel taller than the pane looks like.
	adopt(t, driver, id, socket, panes[0])

	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize an adopted pane: %v", err)
	}
	width, height := paneSize(t, socket, panes[0])
	if width != 132 {
		t.Fatalf("adopted pane width = %d, want the panel's 132", width)
	}
	// The window is 60 rows and this pane shares it with the split's other
	// half, so the pane takes what the split leaves it -- but it has to have
	// grown into the taller window rather than kept its old 11 or 12 rows.
	if height < 24 {
		t.Fatalf("adopted pane height = %d, want it grown into the 60-row window", height)
	}
	if got := foreignWindowSize(t, socket, panes[0]); got != "manual" {
		t.Fatalf("after Resize, the adopted window's window-size = %q, want manual", got)
	}
}

// Resize pins the adopted window to the panel's size, so PrepareAttach has to
// hand it back before a client attaches. Without that the person attaching to
// their own window is stuck at the manager's preview width, with tmux's dotted
// out-of-bounds overlay filling the rest of their terminal.
func TestPrepareAttachRestoresAnAdoptedWindow(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("adoptattach")
	adopt(t, driver, id, socket, panes[0])

	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize an adopted pane: %v", err)
	}
	if got := foreignWindowSize(t, socket, panes[0]); got != "manual" {
		t.Fatalf("after Resize, the adopted window's window-size = %q, want manual", got)
	}

	if err := driver.PrepareAttach(id); err != nil {
		t.Fatalf("PrepareAttach on an adopted session: %v", err)
	}
	want := "latest"
	if driver.attachSizeLargest.Load() {
		want = "largest"
	}
	if got := foreignWindowSize(t, socket, panes[0]); got != want {
		t.Fatalf("after PrepareAttach, the adopted window's window-size = %q, want %q", got, want)
	}
}

// The scan adopts panes by what is running in them, and the manager is often
// started from a pane an agent already holds, so its own window can land on
// its own board. Sizing that one shrinks the terminal the manager is painting,
// and the window-size message that comes back sizes the panel smaller again.
func TestResizeLeavesTheManagersOwnPaneAlone(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := foreignServer(t)
	id := uniqueID("ownpane")
	adopt(t, driver, id, socket, panes[0])
	// What tmux sets for a process running inside that very pane.
	t.Setenv("TMUX_PANE", panes[0])
	t.Setenv("TMUX", "/tmp/tmux-1000/"+socket+",1234,0")

	wantWidth, wantHeight := paneSize(t, socket, panes[0])
	if err := driver.Resize(id, 132, 60); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	width, height := paneSize(t, socket, panes[0])
	if width != wantWidth || height != wantHeight {
		t.Fatalf("the manager's own pane was resized from %dx%d to %dx%d",
			wantWidth, wantHeight, width, height)
	}
	if got := foreignWindowSize(t, socket, panes[0]); got == "manual" {
		t.Fatal("the manager's own window was pinned to the preview panel's size")
	}
}
