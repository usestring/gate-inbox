package singleton

import (
	"bufio"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestHolderInReadsTheFlockOnTheInode(t *testing.T) {
	table := strings.Join([]string{
		"1: POSIX  ADVISORY  WRITE 3178680 08:01:6389347 0 EOF",
		"2: -> FLOCK  ADVISORY  WRITE 999 08:01:6389347 0 EOF",
		"3: FLOCK  ADVISORY  WRITE 777 08:02:6389347 0 EOF",
		"156: FLOCK  ADVISORY  WRITE 3001540 08:01:6389347 0 EOF",
	}, "\n")
	pid, err := holderIn(bufio.NewScanner(strings.NewReader(table)), "08:01", 6389347)
	if err != nil || pid != 3001540 {
		t.Fatalf("holderIn = %d, %v; want 3001540 (not the POSIX lock, the waiter, or the other device)", pid, err)
	}
}

func TestHolderInWithNoHolder(t *testing.T) {
	_, err := holderIn(bufio.NewScanner(strings.NewReader("1: FLOCK  ADVISORY  WRITE 5 08:01:1 0 EOF\n")), "08:01", 2)
	if !errors.Is(err, ErrNoHolder) {
		t.Fatalf("err = %v, want ErrNoHolder", err)
	}
}

func TestHolderNamesThisProcessWhileItHoldsTheLock(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/locks")
	}
	dir := t.TempDir()
	if _, err := Holder(dir); !errors.Is(err, ErrNoHolder) {
		t.Fatalf("before any lock: err = %v, want ErrNoHolder", err)
	}
	lock, _, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := Holder(dir)
	if err != nil || pid != os.Getpid() {
		t.Fatalf("Holder = %d, %v; want %d", pid, err, os.Getpid())
	}
	lock.Release()
	if _, err := Holder(dir); !errors.Is(err, ErrNoHolder) {
		t.Fatalf("after release: err = %v, want ErrNoHolder", err)
	}
}
