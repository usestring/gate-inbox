package tmux

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDescendantsWalksTheTreeFromTheRoots(t *testing.T) {
	table := parseProcessTable(`
    1     0 Sat Oct 31 05:00:00 2026
  100     1 Sat Oct 31 05:00:01 2026
  101   100 Sat Oct 31 05:00:02 2026
  102   101 Sat Oct 31 05:00:03 2026
  200     1 Sat Oct 31 05:00:04 2026
`)
	got := descendants(table, []int{100, 999})
	var pids []int
	for _, p := range got {
		pids = append(pids, p.pid)
	}
	slices.Sort(pids)
	if !slices.Equal(pids, []int{100, 101, 102}) {
		t.Fatalf("descendants = %v, want the root and its tree only", pids)
	}
	if got[0].started != "Sat Oct 31 05:00:01 2026" {
		t.Fatalf("started = %q", got[0].started)
	}
}

// An agent's tool call runs in a session of its own and shrugs off the
// hangup tmux sends when the window closes. Kill must still end it rather
// than leave it running under init.
func TestKillEndsAChildThatIgnoresTheHangup(t *testing.T) {
	driver := requireTmux(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "agent.sh")
	child := `use POSIX; POSIX::setsid(); $SIG{HUP}="IGNORE"; $SIG{TERM}="IGNORE"; ` +
		`open my $f, ">", $ARGV[0]; print $f $$; close $f; sleep 600`
	if err := os.WriteFile(script, []byte("perl -e '"+child+"' \""+pidFile+"\" &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := createManaged(t, driver, "killreap", "sh "+script)
	var pid int
	for deadline := time.Now().Add(5 * time.Second); pid == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the child never wrote its pid")
		}
		raw, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })

	if err := driver.Kill(id); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	AwaitReaps()
	for deadline := time.Now().Add(time.Second); ; time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d ignored the hangup and SIGTERM and survived Kill", pid)
		}
	}
}
