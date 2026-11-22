//go:build darwin

package sysstat

import (
	"os"
	"slices"
	"testing"
	"time"
)

func TestParseProcargs2ReadsEveryArgument(t *testing.T) {
	argc := func(n byte) []byte { return []byte{n, 0, 0, 0} }
	for _, tc := range []struct {
		name string
		raw  []byte
		want []string
	}{
		{"argv after padded exec path", append(argc(3), "/bin/sh\x00\x00\x00sh\x00-c\x00echo hi\x00PATH=/bin\x00"...), []string{"sh", "-c", "echo hi"}},
		{"stops at argc, before the environment", append(argc(1), "/bin/sleep\x00sleep\x0060\x00"...), []string{"sleep"}},
		{"last argument without a terminator", append(argc(2), "/x\x00a\x00b"...), []string{"a", "b"}},
		{"no arguments", append(argc(0), "/x\x00"...), nil},
		{"truncated header", []byte{1, 0}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseProcargs2(tc.raw); !slices.Equal(got, tc.want) {
				t.Fatalf("parseProcargs2 = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestArgvIsThisProcesssArguments(t *testing.T) {
	if got := Argv(os.Getpid()); !slices.Equal(got, os.Args) {
		t.Fatalf("Argv(self) = %q, want %q", got, os.Args)
	}
}

func TestProcTableListsThisProcessUnderItsParent(t *testing.T) {
	table, ok := ReadProcTable()
	if !ok {
		t.Fatal("ReadProcTable failed")
	}
	if !slices.Contains(table.Children[os.Getppid()], os.Getpid()) {
		t.Fatalf("pid %d is not listed under its parent %d", os.Getpid(), os.Getppid())
	}
	start := time.UnixMicro(int64(table.StartMicros[os.Getpid()]))
	if since := time.Since(start); since < 0 || since > 24*time.Hour {
		t.Fatalf("this process's start time %v is not in the recent past", start)
	}
	if table.Has(0) {
		t.Fatal("the kernel's pid 0 is in the table, where a walk from it would never end")
	}
	var walked []int
	table.Walk(os.Getppid(), func(pid int) { walked = append(walked, pid) })
	if !slices.Contains(walked, os.Getpid()) {
		t.Fatalf("a walk from the parent missed this process: %v", walked)
	}
}

// ReadTaskRusage's CPU must be in seconds: the timebase on Apple silicon is
// 125/3 ns per tick, and a missed conversion reads 41x high.
func TestTaskRusageAgreesWithTaskInfo(t *testing.T) {
	for end := time.Now().Add(200 * time.Millisecond); time.Now().Before(end); {
	}
	ru, ok := ReadTaskRusage(os.Getpid())
	if !ok {
		t.Fatal("ReadTaskRusage(self) failed")
	}
	cpu, rss := taskUsage(os.Getpid())
	if ratio := ru.CPUSeconds / cpu; ratio < 0.8 || ratio > 1.25 {
		t.Errorf("rusage CPU %vs, task_info CPU %vs: the timebase conversion is wrong", ru.CPUSeconds, cpu)
	}
	if ru.Resident == 0 || ru.Footprint == 0 || float64(ru.Resident)/float64(rss) < 0.5 {
		t.Errorf("rusage memory resident=%d footprint=%d, task_info RSS %d", ru.Resident, ru.Footprint, rss)
	}
	if _, ok := ReadTaskRusage(1 << 30); ok {
		t.Error("a pid that does not exist read as a process")
	}
}

func TestHostMemoryIsWithinRAM(t *testing.T) {
	total, available, ok := HostMemory()
	if !ok || total == 0 || available > total {
		t.Fatalf("HostMemory = %d of %d, ok=%v", available, total, ok)
	}
}
