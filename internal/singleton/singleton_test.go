package singleton

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain doubles as the incumbent: run with SINGLETON_HOLD set it takes the
// lock, reports it, and either exits on SIGTERM or ignores it.
func TestMain(m *testing.M) {
	dir := os.Getenv("SINGLETON_HOLD")
	if dir == "" {
		tmuxtest.Main(m)
	}
	// Importing tmuxtest unset the pane this holder runs in; a holder started
	// inside one hands it over under names the isolation leaves alone.
	if pane := os.Getenv("SINGLETON_TMUX_PANE"); pane != "" {
		os.Setenv("TMUX", os.Getenv("SINGLETON_TMUX"))
		os.Setenv("TMUX_PANE", pane)
	}
	if _, _, err := Acquire(dir); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(2)
	}
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	os.Stdout.WriteString("held\n")
	for range term {
		if os.Getenv("SINGLETON_IGNORE_TERM") != "1" {
			os.Exit(143)
		}
	}
}

func startHolder(t *testing.T, dir string, ignoreTerm bool) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=XXX_NONE")
	cmd.Env = append(tmuxtest.Environ(), "SINGLETON_HOLD="+dir)
	if ignoreTerm {
		cmd.Env = append(cmd.Env, "SINGLETON_IGNORE_TERM=1")
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	buf := make([]byte, 8)
	if _, err := out.Read(buf); err != nil || !strings.HasPrefix(string(buf), "held") {
		t.Fatalf("holder did not start: %q %v", buf, err)
	}
	return cmd
}

func TestFreeDirIsClaimedWithoutATakeover(t *testing.T) {
	dir := t.TempDir()
	lock, took, err := Acquire(dir)
	if err != nil || took != nil {
		t.Fatalf("Acquire: took=%v err=%v", took, err)
	}
	defer lock.Release()
	if lockedPID(t, dir) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock file names %q, want own pid", lockedPID(t, dir))
	}
}

func TestIncumbentIsTerminatedAndSuperseded(t *testing.T) {
	dir := t.TempDir()
	holder := startHolder(t, dir, false)
	start := time.Now()
	lock, took, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()
	if took == nil || took.PID != holder.Process.Pid || took.Killed {
		t.Fatalf("takeover = %+v, want SIGTERM of pid %d", took, holder.Process.Pid)
	}
	if time.Since(start) > termGrace {
		t.Fatal("takeover waited the whole grace period on a cooperative holder")
	}
	if err := holder.Wait(); err == nil {
		t.Fatal("holder exited cleanly; expected a signal exit")
	}
	if lockedPID(t, dir) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock file names %q after takeover, want own pid", lockedPID(t, dir))
	}
}

func TestStubbornIncumbentIsKilled(t *testing.T) {
	dir := t.TempDir()
	holder := startHolder(t, dir, true)
	lock, took, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()
	if took == nil || took.PID != holder.Process.Pid || !took.Killed {
		t.Fatalf("takeover = %+v, want SIGKILL of pid %d", took, holder.Process.Pid)
	}
	_ = holder.Wait()
}

func TestReleaseFreesTheClaim(t *testing.T) {
	dir := t.TempDir()
	first, _, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	first.Release()
	second, took, err := Acquire(dir)
	if err != nil || took != nil {
		t.Fatalf("second Acquire after Release: took=%v err=%v", took, err)
	}
	second.Release()
}

func lockedPID(t *testing.T, dir string) string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	return strings.TrimSpace(string(data))
}

func TestSupersededPaneIsClosed(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	socket := tmuxtest.Socket(t, "singleton")
	t.Cleanup(func() { tmuxtest.KillServer(socket) })
	tmux := func(args ...string) (string, error) {
		out, err := exec.Command("tmux", append([]string{"-L", socket}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	// Keeps the server up once the board's pane, and with it its session,
	// is gone.
	if _, err := tmux("new-session", "-d", "-s", "keep", "sleep 600"); err != nil {
		t.Fatal(err)
	}
	// The shell the operator launched the board from, left behind when it exits.
	board := fmt.Sprintf(`SINGLETON_HOLD='%s' SINGLETON_TMUX="$TMUX" SINGLETON_TMUX_PANE="$TMUX_PANE" '%s' -test.run=XXX_NONE; exec sleep 600`, dir, os.Args[0])
	paneID, err := tmux("new-session", "-d", "-s", "board", "-P", "-F", "#{pane_id}", board)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(termGrace)
	for lockedPID(t, dir) == "" {
		if time.Now().After(deadline) {
			screen, _ := tmux("capture-pane", "-p", "-t", paneID)
			t.Fatalf("the board in the pane never took the lock:\n%s", screen)
		}
		time.Sleep(pollEvery)
	}

	lock, took, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()
	if took == nil || took.Killed || !took.PaneClosed {
		t.Fatalf("takeover = %+v, want a SIGTERM that closed the pane", took)
	}
	panes, err := tmux("list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(strings.Fields(panes), paneID) {
		t.Fatalf("pane %s outlived the board it held; panes: %s", paneID, panes)
	}
}

func TestAPaneOnAServerThatIsGoneIsLeftAlone(t *testing.T) {
	if closePane(pane{socket: "/nonexistent/socket", serverPID: "1", id: "%0"}) {
		t.Fatal("closed a pane on a server that is not there")
	}
}
