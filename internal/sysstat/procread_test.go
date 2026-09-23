package sysstat

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The readers below are the walk exactly as it stood before this change:
// os.ReadFile per file, os.ReadDir per task directory. They are what the new
// walk has to agree with, and they are kept as running code rather than
// described, because a description of a walk cannot be run against a live
// process tree.
func childrenOfViaReadFile(pid int, threads int) []int {
	base := "/proc/" + strconv.Itoa(pid) + "/task"
	if threads <= 1 {
		return childPIDsViaReadFile(base + "/" + strconv.Itoa(pid) + "/children")
	}
	tasks, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []int
	for _, task := range tasks {
		out = append(out, childPIDsViaReadFile(base+"/"+task.Name()+"/children")...)
	}
	return out
}

func childPIDsViaReadFile(path string) []int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []int
	for _, field := range strings.Fields(string(raw)) {
		if child, err := strconv.Atoi(field); err == nil {
			out = append(out, child)
		}
	}
	return out
}

func readProcStatViaReadFile(pid int) (procInfo, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procInfo{}, false
	}
	return parseProcStat(string(raw))
}

// walkViaReadFile is sampleTree's shape over the old readers: the pids it
// visits, which is what "the same walk" means.
func walkViaReadFile(root int) []int {
	rootInfo, ok := readProcStatViaReadFile(root)
	if !ok {
		return nil
	}
	seen := map[int]bool{root: true}
	out := []int{root}
	queue := childrenOfViaReadFile(root, rootInfo.threads)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		info, ok := readProcStatViaReadFile(pid)
		if !ok {
			continue
		}
		out = append(out, pid)
		queue = append(queue, childrenOfViaReadFile(pid, info.threads)...)
	}
	sort.Ints(out)
	return out
}

// walkViaRawReader is the same walk over the shipped readers.
func walkViaRawReader(root int) []int {
	rootInfo, ok := readProcStat(root)
	if !ok {
		return nil
	}
	seen := map[int]bool{root: true}
	out := []int{root}
	queue := childrenOf(root, rootInfo.threads)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		info, ok := readProcStat(pid)
		if !ok {
			continue
		}
		out = append(out, pid)
		queue = append(queue, childrenOf(pid, info.threads)...)
	}
	sort.Ints(out)
	return out
}

// The raw reader is only allowed to be cheaper, never different. Sizes
// either side of the pooled buffer are the interesting ones: a file that
// exactly fills it, and one that does not fit at all.
func TestTheRawReaderReturnsTheSameBytesAsReadFile(t *testing.T) {
	dir := t.TempDir()
	for _, size := range []int{0, 1, 350, procBufSize - 1, procBufSize, procBufSize + 1, 5 * procBufSize} {
		body := make([]byte, size)
		for i := range body {
			body[i] = byte('a' + i%26)
		}
		path := filepath.Join(dir, "f"+strconv.Itoa(size))
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		var got []byte
		if !withProcFile(path, func(raw []byte) { got = append([]byte(nil), raw...) }) {
			t.Fatalf("withProcFile could not read a %d-byte file", size)
		}
		if string(got) != string(want) {
			t.Fatalf("a %d-byte file read back as %d bytes: the raw reader stops short of the file",
				size, len(got))
		}
	}
	if withProcFile(filepath.Join(dir, "no-such-file"), func([]byte) {}) {
		t.Fatal("the raw reader reported a missing file as read; an exited process would be counted as live")
	}
}

// The directory listing has to answer the same task ids os.ReadDir does. A
// missed task is a missed child list, and a missed child is a live agent the
// board reads as gone.
func TestTheRawDirListingAgreesWithReadDir(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	base := "/proc/" + strconv.Itoa(os.Getpid()) + "/task"
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", base, err)
	}
	want := map[string]bool{}
	for _, e := range entries {
		want[e.Name()] = true
	}
	got := map[string]bool{}
	if !procDirNames(base, func(name string) { got[name] = true }) {
		t.Fatalf("procDirNames could not list %s", base)
	}
	// This process's own threads come and go under the runtime, so the
	// comparison is one-directional on the moving side: every task the raw
	// listing reports must be a real one, and every task ReadDir saw that the
	// raw listing did not must be gone by the time it is checked.
	for name := range got {
		if !want[name] {
			if _, err := os.Stat(base + "/" + name); err != nil {
				continue
			}
			t.Errorf("the raw listing reported task %s, which ReadDir did not", name)
		}
	}
	for name := range want {
		if got[name] {
			continue
		}
		if _, err := os.Stat(base + "/" + name); err == nil {
			t.Errorf("the raw listing missed live task %s", name)
		}
	}
	if len(got) == 0 {
		t.Fatal("the raw listing found no tasks at all")
	}
}

// The whole safety argument for the cheaper reader: the walk it feeds visits
// exactly the pids the os.ReadFile walk visits.
func TestTheRawWalkVisitsTheSamePidsAsTheReadFileWalk(t *testing.T) {
	if !procChildren() {
		t.Skip("no /proc child lists on this machine")
	}
	root := treeUnderTest(t)
	// A live tree can change between two walks, so disagreement is only
	// interesting when it survives a re-read: the reference walk runs either
	// side of the one under test and the pids have to match one of them.
	before := walkViaReadFile(root)
	raw := walkViaRawReader(root)
	after := walkViaReadFile(root)
	if len(raw) < 3 {
		t.Fatalf("the raw walk saw %d pids under a tree of three: %v", len(raw), raw)
	}
	if !samePids(raw, before) && !samePids(raw, after) {
		t.Fatalf("the raw walk visited %v; the os.ReadFile walk visited %v then %v", raw, before, after)
	}
	// The numbers, not only the shape: a reader that returned truncated bytes
	// would still walk the right pids and report nonsense for every one.
	for _, pid := range raw {
		want, ok := readProcStatViaReadFile(pid)
		if !ok {
			continue
		}
		got, ok := readProcStat(pid)
		if !ok {
			t.Errorf("the raw reader could not read pid %d, which os.ReadFile just read", pid)
			continue
		}
		if got.threads != want.threads {
			t.Errorf("pid %d: raw reader saw %d threads, os.ReadFile saw %d", pid, got.threads, want.threads)
		}
		// CPU and RSS move while a live process runs, so they are compared
		// for the monotonic relation the read order implies rather than for
		// equality: the raw read happens second.
		if got.cpuSeconds < want.cpuSeconds {
			t.Errorf("pid %d: raw reader saw %vs of CPU, os.ReadFile saw %vs earlier", pid, got.cpuSeconds, want.cpuSeconds)
		}
		if want.rssPages > 0 && got.rssPages == 0 {
			t.Errorf("pid %d: raw reader saw no resident pages, os.ReadFile saw %d", pid, want.rssPages)
		}
	}
}

func samePids(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
