// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmux"
)

func TestGuardedMouseCommandWrapsTheSend(t *testing.T) {
	command, args := guardedMouseCommand("sess-abc:0.0", "\x1b[<64;3;2M")
	if !strings.HasPrefix(command, "if-shell -F -t sess-abc:0.0 '#{mouse_any_flag}' 'send-keys") {
		t.Fatalf("control-pipe command = %q", command)
	}
	if len(args) != 6 || args[0] != "if-shell" || args[4] != "#{mouse_any_flag}" {
		t.Fatalf("fallback args = %q", args)
	}
	// The nested send has to stay one argument, which is why it cannot ride
	// the whitespace-splitting SendRawAt.
	if !strings.HasPrefix(args[5], "send-keys -t ") || !strings.Contains(args[5], " -H ") {
		t.Fatalf("nested command = %q", args[5])
	}
}

// The guard has to be transparent while the application really is tracking
// the mouse, and silent once it has stopped.
func TestGuardedMouseCommandFollowsTheLiveFlag(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	driver, err := tmux.NewWithSocket("gateguard")
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	sessID := "guard-probe"
	if err := driver.Create(sessID, t.TempDir(),
		`printf '\033[?1003h\033[?1006h'; cat`, map[string]string{}, 80, 24); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = driver.Kill(sessID) })
	waitForMouseFlag(t, driver, sessID, "1")

	_, args := guardedMouseCommand(driver.TargetName(sessID), "\x1b[<64;7;3M")
	if err := runTmuxArgs(driver, args); err != nil {
		t.Fatalf("guarded send while tracking: %v", err)
	}
	if !paneGains(t, driver, sessID, "64;7;3M") {
		t.Fatal("a report was dropped while the application was tracking the mouse")
	}

	// cat holds the line until a newline arrives, and only what it echoes
	// reaches the pane's terminal to turn mouse reporting off.
	if err := driver.SendRaw("send-keys -t " + driver.TargetName(sessID) +
		" -H 1b 5b 3f 31 30 30 33 6c 1b 5b 3f 31 30 30 36 6c 0a"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	waitForMouseFlag(t, driver, sessID, "0")

	_, args = guardedMouseCommand(driver.TargetName(sessID), "\x1b[<64;9;4M")
	if err := runTmuxArgs(driver, args); err != nil {
		t.Fatalf("guarded send after tracking stopped: %v", err)
	}
	if paneGains(t, driver, sessID, "64;9;4M") {
		pane, _ := driver.CapturePane(sessID)
		t.Fatalf("a report reached the pane after the application left mouse mode:\n%s", strings.TrimSpace(pane))
	}
}

func runTmuxArgs(driver *tmux.Driver, args []string) error {
	return exec.Command("tmux", append([]string{"-L", driver.SocketName()}, args...)...).Run()
}

func waitForMouseFlag(t *testing.T, driver *tmux.Driver, sessID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("tmux", "-L", driver.SocketName(),
			"display-message", "-p", "-t", driver.TargetName(sessID), "#{mouse_any_flag}").Output()
		if err == nil && strings.TrimSpace(string(out)) == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("mouse_any_flag never reached %q", want)
}

func paneGains(t *testing.T, driver *tmux.Driver, sessID, want string) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pane, err := driver.CapturePane(sessID); err == nil && strings.Contains(pane, want) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}
