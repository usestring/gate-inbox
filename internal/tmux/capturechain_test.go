package tmux

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// chainServer is a server with several panes, each printing a line only it
// prints, so a batched read that crossed two panes' text would be caught.
func chainServer(t *testing.T, panes int) (string, []string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := tmuxtest.NewSocket("chain")
	command := "sh -c \"printf 'pane-marker-%s\\n' \\$N; exec cat\""
	first := strings.Replace(command, "\\$N", "0", 1)
	if out, err := tmuxOn(socket, "new-session", "-d", "-s", "user", "-x", "80", "-y", "24", first).CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { tmuxOn(socket, "kill-server").Run() })
	for i := 1; i < panes; i++ {
		body := strings.Replace(command, "\\$N", string(rune('0'+i)), 1)
		if out, err := tmuxOn(socket, "new-window", "-d", "-t", "user", body).CombinedOutput(); err != nil {
			t.Fatalf("new-window: %v: %s", err, out)
		}
	}
	out, err := tmuxOn(socket, "list-panes", "-a", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("list-panes: %v: %s", err, out)
	}
	ids := strings.Fields(string(out))
	for i, pane := range ids {
		waitForPane(t, socket, pane, "pane-marker-"+string(rune('0'+i)))
	}
	return socket, ids
}

// adoptAll registers each foreign pane under an id of its own, which is what
// the manager holds: captureChain takes ids and resolves each to its target.
func adoptAll(t *testing.T, driver *Driver, socket string, panes []string) []string {
	t.Helper()
	ids := make([]string, 0, len(panes))
	for i, pane := range panes {
		id := uniqueID("chained" + strconv.Itoa(i))
		adopt(t, driver, id, socket, pane)
		ids = append(ids, id)
	}
	return ids
}

// A chained capture returns what the forks it replaces returned, byte for
// byte. Anything less and the board would be painting a frame nobody can
// point at a pane and reproduce.
func TestChainedCaptureMatchesOneForkPerPane(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := chainServer(t, 4)

	ids := adoptAll(t, driver, socket, panes)
	texts, _, err := driver.captureChain(socket, ids)
	if err != nil {
		t.Fatalf("chained capture: %v", err)
	}
	if len(texts) != len(ids) {
		t.Fatalf("chained capture returned %d panes, want %d", len(texts), len(ids))
	}
	for i, pane := range panes {
		out, err := tmuxOn(socket, "capture-pane", "-p", "-e", "-t", pane).Output()
		if err != nil {
			t.Fatalf("forked capture of %s: %v", pane, err)
		}
		if texts[i] != string(out) {
			t.Errorf("%s: chained capture differs from the fork\nchained: %q\nforked:  %q",
				pane, tailOf(texts[i]), tailOf(string(out)))
		}
		if !strings.Contains(texts[i], "pane-marker-"+string(rune('0'+i))) {
			t.Errorf("%s: chained capture holds another pane's text: %q", pane, tailOf(texts[i]))
		}
	}
}

// tmux stops a command list at the first failure, so one dead pane must not
// cost its server the whole pass: the panes before it are kept, it takes the
// error, and the panes behind it are read by the chain that follows.
func TestChainedCaptureKeepsGoingPastADeadPane(t *testing.T) {
	driver := requireTmux(t)
	socket, panes := chainServer(t, 4)

	live := adoptAll(t, driver, socket, panes)
	dead := uniqueID("gone")
	adopt(t, driver, dead, socket, "%99999")
	ids := append([]string{}, live[:2]...)
	ids = append(ids, dead)
	ids = append(ids, live[2:]...)

	out := map[string]Capture{}
	driver.captureServer(socket, ids, out)

	for _, pane := range live {
		capture, listed := out[pane]
		if !listed || capture.Err != nil {
			t.Fatalf("%s: listed=%v err=%v -- a dead pane took a live one down with it", pane, listed, capture.Err)
		}
		if capture.Text == "" {
			t.Fatalf("%s: no text past the dead pane", pane)
		}
	}
	gone, listed := out[dead]
	if !listed || gone.Err == nil {
		t.Fatalf("the dead pane came back as readable: %+v", gone)
	}
	if gone.Text != "" {
		t.Fatalf("the dead pane carried text: %q", gone.Text)
	}
}

// Every batch gets its own separator, so a pane printing the last one cannot
// split a later capture in two.
func TestCaptureSeparatorIsFreshEachTime(t *testing.T) {
	first, err := captureSeparator()
	if err != nil {
		t.Fatal(err)
	}
	second, err := captureSeparator()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two batches shared a separator: %q", first)
	}
}

// TestRealBoardChainedCaptureCost measures the change against the operator's
// live board: one fork per pane, which is what a poll pass used to cost,
// against one chained fork per server, which is what it costs now.
//
// Read-only on the operator's panes -- capture-pane and nothing else -- and
// gated, because it is a measurement rather than an assertion.
func TestRealBoardChainedCaptureCost(t *testing.T) {
	if os.Getenv("BOARD_MEASURE") == "" {
		t.Skip("set BOARD_MEASURE=1 to measure the live board")
	}
	out, err := exec.Command("tmux", "-L", DefaultSocket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Skipf("no live board: %v", err)
	}
	panes := strings.Fields(string(out))
	if len(panes) == 0 {
		t.Skip("no panes on the live board")
	}

	for pass := 1; pass <= 3; pass++ {
		mark := time.Now()
		for _, pane := range panes {
			exec.Command("tmux", "-L", DefaultSocket, "capture-pane", "-p", "-e", "-t", pane).Output()
		}
		perPane := time.Since(mark)

		sep, err := captureSeparator()
		if err != nil {
			t.Fatal(err)
		}
		args := []string{"-L", DefaultSocket}
		for i, pane := range panes {
			if i > 0 {
				args = append(args, ";")
			}
			args = append(args, "capture-pane", "-p", "-e", "-t", pane, ";", "display-message", "-p", sep)
		}
		mark = time.Now()
		chained, chainErr := exec.Command("tmux", args...).Output()
		chainTime := time.Since(mark)

		t.Logf("pass %d over %d live panes: one fork per pane %v, one chained fork %v (%.1fx), %d panes answered, err=%v",
			pass, len(panes), perPane.Round(time.Millisecond), chainTime.Round(time.Millisecond),
			float64(perPane)/float64(chainTime), strings.Count(string(chained), sep), chainErr)
	}
}
