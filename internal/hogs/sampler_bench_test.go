package hogs

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// smapsRollup is a real smaps_rollup's shape: the sampler reads one line of
// it, but the kernel renders all of these.
const smapsRollup = `55d0c0000000-7ffd00000000 ---p 00000000 00:00 0                          [rollup]
Rss:             %d kB
Pss:             %d kB
Pss_Dirty:        1024 kB
Pss_Anon:         1024 kB
Pss_File:          512 kB
Pss_Shmem:           0 kB
Shared_Clean:      512 kB
Shared_Dirty:        0 kB
Private_Clean:     256 kB
Private_Dirty:    1024 kB
Referenced:       2048 kB
Anonymous:        1024 kB
KSM:                 0 kB
LazyFree:            0 kB
AnonHugePages:       0 kB
ShmemPmdMapped:      0 kB
FilePmdMapped:       0 kB
Shared_Hugetlb:      0 kB
Private_Hugetlb:     0 kB
Swap:                0 kB
SwapPss:             0 kB
Locked:              0 kB
`

// benchBoard lays down sessions × procs processes, each tree a root with a
// fan of children and grandchildren, and every fifth process multi-threaded
// the way a runtime is.
func benchBoard(b *testing.B, sessions, procs int) (string, map[string]int) {
	b.Helper()
	root := b.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	write("uptime", "1000.00 0.00\n")
	write("meminfo", "MemTotal: 100000000 kB\nMemAvailable: 50000000 kB\n")
	roots := map[string]int{}
	pid := 1000
	for s := range sessions {
		first := pid
		roots[fmt.Sprintf("s%02d", s)] = first
		children := map[int][]int{}
		for i := 1; i < procs; i++ {
			parent := first + (i-1)/8
			children[parent] = append(children[parent], first+i)
		}
		for i := range procs {
			p := first + i
			threads := 1
			if i%5 == 0 {
				threads = 8
			}
			ps := strconv.Itoa(p)
			write(ps+"/stat", fmt.Sprintf("%d (node) S %d 0 0 0 -1 0 0 0 0 0 %d %d 0 0 20 0 %d 0 500 0 %d 0 0\n", p, p-1, i*3, i, threads, 2000+i))
			write(ps+"/cmdline", "node\x00/usr/lib/node_modules/some/tool.js\x00--flag\x00")
			write(ps+"/smaps_rollup", fmt.Sprintf(smapsRollup, 8000+i, 6000+i))
			var kids []string
			for _, c := range children[p] {
				kids = append(kids, strconv.Itoa(c))
			}
			for t := range threads {
				tid := ps
				if t > 0 {
					tid = strconv.Itoa(p*100 + t)
				}
				body := ""
				if t == 0 {
					body = strings.Join(kids, " ")
				}
				write(ps+"/task/"+tid+"/children", body)
			}
		}
		pid += procs
	}
	return root, roots
}

// BenchmarkSample is one watcher sample over a board of 30 sessions of 200
// processes each: 6,000 processes, well past any board this runs on. What it
// measures is the sampler's own work -- opens, reads, parsing, allocation --
// on a tmpfs, not the kernel's cost of rendering a live process's
// smaps_rollup; the Sampler's own doc comment has that measurement, and it is
// why the second case is the one production avoids.
func BenchmarkSample(b *testing.B) {
	root, roots := benchBoard(b, 30, 200)
	for _, bc := range []struct {
		name  string
		floor uint64
	}{
		// Every tree under the smallest size rule: memory is stat's RSS.
		{"rss-screened", math.MaxUint64},
		// Every tree over it: every process's smaps_rollup is read too.
		{"pss-every-tree", 0},
	} {
		b.Run(bc.name, func(b *testing.B) {
			s := NewSampler(root)
			s.SetPSSFloor(bc.floor)
			at := time.Unix(1_000_000, 0)
			s.Sample(at, roots)
			b.ResetTimer()
			for i := range b.N {
				s.Sample(at.Add(time.Duration(i+1)*10*time.Second), roots)
			}
			b.ReportMetric(float64(30*200), "procs/op")
		})
	}
}
