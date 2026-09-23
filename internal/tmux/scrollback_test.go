package tmux

import (
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// The naming sweep reads panes it knows only as a socket and a "%id", and it
// wants the scrollback rather than the visible screen. Batching that over the
// control pipe is only worth having if it returns exactly what the fork it
// replaces returned, including the lines that have already scrolled off.
func TestCaptureScrollbackMatchesTheForkedReadIncludingHistory(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := scrollbackServer(t)
	t.Cleanup(driver.CloseCaptureClients)

	const scrolled = "scrolled-off-marker"
	for _, pane := range panes {
		waitForPane(t, socket, pane, "filler-60")
	}

	got := driver.CaptureScrollback(socket, panes, 200)
	if len(got) != len(panes) {
		t.Fatalf("captured %d panes, want %d", len(got), len(panes))
	}
	for i, pane := range panes {
		capture, listed := got[pane]
		if !listed || capture.Err != nil {
			t.Fatalf("%s: listed=%v err=%v", pane, listed, capture.Err)
		}
		marker := scrolled + "-" + strconv.Itoa(i)
		if !strings.Contains(capture.Text, marker) {
			t.Errorf("%s: batched capture lost the scrollback (no %q); the -S window is not reaching tmux", pane, marker)
		}
		// Word for word against the fork it replaces, because a batched read
		// that quietly returned a different window would rename sessions off
		// text the sweep never used to see.
		//
		// Words, not bytes: the fork keeps the pane's trailing empty line and
		// the control block does not, and the sweep's only consumer is
		// convo.Normalize, which joins strings.Fields. Asserting the byte the
		// product discards would pin a difference nothing depends on.
		args := []string{"-L", socket, "capture-pane", "-p", "-S", "-200", "-t", pane}
		out, err := exec.Command("tmux", args...).Output()
		if err != nil {
			t.Fatalf("forked capture of %s: %v", pane, err)
		}
		batched := strings.Join(strings.Fields(capture.Text), " ")
		forked := strings.Join(strings.Fields(string(out)), " ")
		if batched != forked {
			t.Errorf("%s: batched capture differs from the forked read\nbatched %d words:\n%q\nforked %d words:\n%q",
				pane, len(strings.Fields(capture.Text)), tailOf(batched), len(strings.Fields(string(out))), tailOf(forked))
		}
	}
}

// A server with no control client still has to answer, or a sweep would stop
// naming every pane on it.
func TestCaptureScrollbackFallsBackToForkingWithoutAClient(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := scrollbackServer(t)
	t.Cleanup(driver.CloseCaptureClients)

	// Hold the server off the way a failed client open does, which is the
	// state that sends every read down the fork path.
	driver.captures.mu.Lock()
	driver.captures.holdLocked(socket, true)
	driver.captures.mu.Unlock()

	got := driver.CaptureScrollback(socket, panes, 200)
	for _, pane := range panes {
		capture, listed := got[pane]
		if !listed || capture.Err != nil {
			t.Fatalf("%s: the fork fallback returned nothing: listed=%v err=%v", pane, listed, capture.Err)
		}
		if capture.Text == "" {
			t.Errorf("%s: fork fallback returned empty text", pane)
		}
	}
}

// scrollbackServer is a private tmux server whose panes have each already
// printed more than a screenful, so the marker at the top is reachable only
// through the scrollback. The panes print it themselves rather than being sent
// keys: foreignServer's panes run cat, which echoes a script instead of
// running it.
func scrollbackServer(t *testing.T) (string, []string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := tmuxtest.NewSocket("scrollback")
	command := "sh -c \"printf 'scrolled-off-marker-%s\\n' \\$N; " +
		"for n in \\$(seq 1 60); do printf 'filler-%s\\n' \\$n; done; exec cat\""

	first := strings.Replace(command, "\\$N", "0", 1)
	if out, err := tmuxOn(socket, "new-session", "-d", "-s", "user", "-x", "80", "-y", "24", first).CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { tmuxOn(socket, "kill-server").Run() })
	second := strings.Replace(command, "\\$N", "1", 1)
	if out, err := tmuxOn(socket, "split-window", "-t", "user", second).CombinedOutput(); err != nil {
		t.Fatalf("split-window: %v: %s", err, out)
	}
	out, err := tmuxOn(socket, "list-panes", "-t", "user", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes: %v: %s", err, out)
	}
	panes := strings.Fields(string(out))
	if len(panes) != 2 {
		t.Fatalf("want 2 panes, got %q", panes)
	}
	return socket, panes
}

func tailOf(s string) string {
	if len(s) <= 200 {
		return s
	}
	return "..." + s[len(s)-200:]
}
