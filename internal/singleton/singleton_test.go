package singleton

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain doubles as the incumbent: run with SINGLETON_HOLD set it takes the
// lock, reports it, and either exits on SIGTERM or ignores it.
func TestMain(m *testing.M) {
	dir := os.Getenv("SINGLETON_HOLD")
	if dir == "" {
		os.Exit(m.Run())
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
	cmd.Env = append(os.Environ(), "SINGLETON_HOLD="+dir)
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
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock file names %q, want own pid", data)
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
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock file names %q after takeover, want own pid", data)
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
