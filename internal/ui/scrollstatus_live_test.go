package ui

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// TestLiveScrolledPaneKeepsItsStatus is the only proof that a real agent
// scrolling its own viewport no longer moves the row it is on. It drives a
// real claude in a pane on a socket of its own, has it print a reply that
// quotes a spinner and an interrupt hint (any session working on terminal
// tooling produces exactly that), waits for the turn to end, then forwards
// wheel-up notches until the visible screen is showing that quoted text
// from a few hundred lines ago. Before the displaced-viewport guard the
// pane read working there, minutes after the turn was over.
//
// It spends tokens against whatever account the machine is signed in to,
// so it runs only when asked for by name.
//
//	GATE_INBOX_LIVE_SCROLL=1 go test ./internal/ui/ \
//	  -run TestLiveScrolledPaneKeepsItsStatus -v -timeout 10m
func TestLiveScrolledPaneKeepsItsStatus(t *testing.T) {
	if os.Getenv("GATE_INBOX_LIVE_SCROLL") == "" {
		t.Skip("live scroll: set GATE_INBOX_LIVE_SCROLL=1 to run a real agent")
	}
	for _, tool := range []string{"tmux", "claude"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}

	// A socket of its own, so nothing here can reach the operator's board.
	socket := newTestSocket()
	const target = "byhand"
	cwd := t.TempDir()
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	create := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", target,
		"-c", cwd, "-x", "120", "-y", "40", "claude")
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("start the pane: %v: %s", err, out)
	}
	answerTrustDialog(t, socket, target)

	m := buildModel(t)
	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	if m.poller.engine, err = status.NewEngine(cfg); err != nil {
		t.Fatalf("status engine: %v", err)
	}
	// The shipped claude rules, without the hook file that normally answers
	// ahead of them: the pane heuristic is what is under test.
	sess := store.Session{ID: "livescroll", Tool: "claude", Status: status.Idle}

	typeLine(t, socket, target, "Reply with exactly these lines and nothing else, and run no tools: "+
		"first a line reading (star) Drizzling... (6s . thinking) written with a real asterisk, "+
		"then a line reading   esc to interrupt, then the numbers 1 to 90 one per line.")
	waitForQuietTurn(t, socket, target)

	live := capture(t, socket, target)
	t.Logf("live bottom:\n%s", live)
	seedRegionHash(t, m, sess, live)
	if got := deriveStatus(t, m, sess, live, true); got == status.Working {
		t.Fatalf("the turn is over at the live bottom, so it must not read working: %s", live)
	}

	scrolled := scrollBackTo(t, socket, target, "esc to interrupt")
	t.Logf("scrolled-back screen:\n%s", scrolled)
	seedRegionHash(t, m, sess, live)
	got := deriveStatus(t, m, sess, scrolled, true)
	t.Logf("derivePaneStatus over the scrolled-back screen: %q", got)
	if got == status.Working {
		t.Fatal("a spinner scrolled up from a finished turn must not read working")
	}
	if got != status.Idle {
		t.Fatalf("a displaced viewport must hold the stored status (idle), got %q", got)
	}
}

// scrollBackTo forwards wheel-up notches until the pane's visible screen is
// showing both the tool's displaced-viewport affordance and want. The
// reports are the same bytes sendFocusReport puts on the wire: a pointer
// move ahead of the notch, since claude tracks all motion.
func scrollBackTo(t *testing.T, socket, target, want string) string {
	t.Helper()
	report := sgrMouse(motionButton, 40, 20) + sgrMouse(wheelUpButton, 40, 20)
	for notch := 0; notch < 300; notch++ {
		sendRaw(t, socket, target, report)
		if notch%5 != 4 {
			continue
		}
		time.Sleep(150 * time.Millisecond)
		screen := capture(t, socket, target)
		if strings.Contains(screen, "Jump to bottom") && strings.Contains(screen, want) {
			return screen
		}
	}
	t.Fatalf("never scrolled back to %q:\n%s", want, capture(t, socket, target))
	return ""
}

// waitForQuietTurn blocks until the reply has landed and the pane has
// stopped changing, which is the turn ending: the reply asked for runs no
// tools and prints no more once done.
func waitForQuietTurn(t *testing.T, socket, target string) {
	t.Helper()
	waitForScreen(t, socket, target, "the turn never printed its end line", func(screen string) bool {
		return strings.Contains(screen, "\u00b7 done")
	})
	deadline := time.Now().Add(3 * time.Minute)
	previous, quiet := "", 0
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		screen := capture(t, socket, target)
		if screen == previous {
			if quiet++; quiet >= 3 {
				return
			}
			continue
		}
		previous, quiet = screen, 0
	}
	t.Fatalf("the turn never went quiet:\n%s", capture(t, socket, target))
}

// typeLine delivers a prompt and submits it. The text goes in as literal
// bytes so the punctuation in it cannot be read as a tmux key name, and it
// is not submitted until the pane proves it took the bytes: a claude that
// has drawn its input box is still not always reading it.
func typeLine(t *testing.T, socket, target, text string) {
	t.Helper()
	waitForScreen(t, socket, target, "claude never drew its input box", func(screen string) bool {
		return strings.Contains(screen, "auto mode") || strings.Contains(screen, "shortcuts")
	})
	for attempt := 0; attempt < 5; attempt++ {
		sendRaw(t, socket, target, text)
		time.Sleep(2 * time.Second)
		// The input box wraps and indents, so the typed line is only ever
		// itself again with the whitespace taken back out.
		if strings.Contains(unspaced(capture(t, socket, target)), unspaced(text)) {
			tmuxOut(t, socket, "send-keys", "-t", target, "Enter")
			return
		}
		tmuxOut(t, socket, "send-keys", "-t", target, "C-u")
	}
	t.Fatalf("the prompt never reached the input box:\n%s", capture(t, socket, target))
}

func waitForScreen(t *testing.T, socket, target, complaint string, ready func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if ready(capture(t, socket, target)) {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("%s:\n%s", complaint, capture(t, socket, target))
}

// sendRaw delivers literal bytes to a pane. tmux takes one hex value per
// argument, so the joined form sendFocusReport builds for a control-mode
// command line has to be split back out here.
func sendRaw(t *testing.T, socket, target, payload string) {
	t.Helper()
	args := []string{"send-keys", "-t", target, "-H"}
	args = append(args, strings.Fields(hexBytes(payload))...)
	tmuxOut(t, socket, args...)
}

func unspaced(s string) string { return strings.Join(strings.Fields(s), "") }

func capture(t *testing.T, socket, target string) string {
	t.Helper()
	return strings.TrimRight(tmuxOut(t, socket, "capture-pane", "-p", "-t", target), "\n") + "\n"
}
