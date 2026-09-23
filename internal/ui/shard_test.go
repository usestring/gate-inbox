package ui

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// This package is ~1500 tests that spend their time blocked on tmux round
// trips rather than computing, and none of them can run under t.Parallel: the
// render memo, theme and glyph set are package-level variables New() rewrites
// for every Model (README.md has the measurement). Run serially, that made
// internal/ui the whole wall clock of `go test ./...` -- 233s to 367s on a
// loaded box while every other package finished inside 90s.
//
// So a plain run of this package shards itself: TestMain re-executes the test
// binary as several processes, each with its own copy of those globals and
// its own tmux server, and then runs the tests that need a quiet box alone.
// It is scripts/shard-test.sh's two-phase run, moved to where `go test ./...`
// reaches it without anyone having to remember a script.
//
// A run that selects tests itself -- -run, -skip, -list, a benchmark, a
// coverage profile -- is left exactly as Go would run it, and so is every
// child, which is how the re-execution cannot recurse.

// shardChildEnv marks a process as one shard of a parent's run.
const shardChildEnv = "GATE_INBOX_UI_SHARD"

// shardCountEnv overrides the shard count; 1 turns sharding off.
const shardCountEnv = "GATE_INBOX_UI_SHARDS"

// shardCount is one shard per core, the default scripts/shard-test.sh
// settled on: more shards shrink the bulk phase a little and have been seen
// to push the delivery-latency tests in it over their budgets.
func shardCount() int {
	if v := os.Getenv(shardCountEnv); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return 1
		}
		return n
	}
	return runtime.NumCPU()
}

// shouldShard reports whether this process is a parent run that selected
// nothing itself. flag.Parse must have run.
func shouldShard() bool {
	if os.Getenv(shardChildEnv) != "" || shardCount() < 2 {
		return false
	}
	for _, name := range []string{
		"test.run", "test.skip", "test.list", "test.bench", "test.fuzz",
		"test.coverprofile", "test.cpuprofile", "test.memprofile",
		"test.blockprofile", "test.mutexprofile", "test.trace",
	} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return false
		}
	}
	_, err := exec.LookPath("tmux")
	return err == nil
}

// frameMarker is the byte -test.v=test2json puts in front of each line it
// frames, so a reader can tell the harness's lines from a test's output.
const frameMarker = "\x16"

// skipLine matches a skip in -test.v output and captures the top-level test
// it belongs to. A subtest that needs a quiet box cannot be run on its own
// here, so its whole test goes to the quiet phase instead.
var skipLine = regexp.MustCompile(`^\s*--- SKIP: ([^\s/]+)(?:/\S*)? \(`)

// runSharded is TestMain's run when shouldShard says so. It returns the exit
// code and writes the same kind of stream a serial run would: each shard's
// output whole, one after another, so -json and -v readers see tests that
// never interleave, then the quiet phase from this process's own m.Run.
func runSharded(m *testing.M) int {
	names, err := listTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ui shards: listing tests: %v; running serially\n", err)
		return m.Run()
	}
	shards := shardCount()
	if shards > len(names) {
		shards = len(names)
	}
	// Dealt round-robin, not in blocks: neighbouring names are usually one
	// file, so a block puts a slow file's tests all in one shard.
	parts := make([][]string, shards)
	for i, name := range names {
		parts[i%shards] = append(parts[i%shards], name)
	}

	verbose := flag.Lookup("test.v").Value.String()
	userShort := testing.Short()
	var args []string
	for _, arg := range os.Args[1:] {
		// go test hands the binary a log of the files and environment it
		// read, and keys its test cache on it. Each shard writes its own,
		// below, and they are merged into this one afterwards: a shard
		// writing the parent's would truncate it, and a shard writing none
		// would let a changed golden come back "ok (cached)".
		if strings.HasPrefix(arg, "-test.testlogfile") {
			continue
		}
		args = append(args, arg)
	}
	logPath := flag.Lookup("test.testlogfile").Value.String()
	var shardLogs []string
	if logPath != "" {
		dir, err := os.MkdirTemp("", "ui-shard-logs")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ui shards: %v; running serially\n", err)
			return m.Run()
		}
		defer os.RemoveAll(dir)
		for i := range parts {
			shardLogs = append(shardLogs, filepath.Join(dir, fmt.Sprintf("shard-%d.log", i)))
		}
	}
	// The shards always run verbose: a passing or skipped test prints
	// nothing otherwise, and the skips are how the quiet phase learns its
	// names. A parent that was not asked for -v prints a shard's output only
	// when the shard failed.
	if verbose != "true" && verbose != "test2json" {
		args = append(args, "-test.v=true")
	}
	// A test that needs a quiet box says so with testing.Short(); under
	// -short in the shards it skips, and the quiet phase below runs exactly
	// those again, alone and in full.
	if !userShort {
		args = append(args, "-test.short")
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		failed bool
		quiet  = map[string]bool{}
	)
	for i, part := range parts {
		wg.Add(1)
		go func(i int, part []string) {
			defer wg.Done()
			var out bytes.Buffer
			shardArgs := append(append([]string{}, args...), "-test.run=^("+strings.Join(part, "|")+")$")
			if shardLogs != nil {
				shardArgs = append(shardArgs, "-test.testlogfile="+shardLogs[i])
			}
			cmd := exec.Command(os.Args[0], shardArgs...)
			cmd.Env = append(os.Environ(), shardChildEnv+"=1")
			cmd.Stdout, cmd.Stderr = &out, &out
			runErr := cmd.Run()

			mu.Lock()
			defer mu.Unlock()
			if runErr != nil {
				failed = true
			}
			var kept bytes.Buffer
			for _, line := range strings.SplitAfter(out.String(), "\n") {
				bare := strings.TrimSpace(strings.TrimPrefix(line, frameMarker))
				// Each shard's own verdict; this process prints the one
				// that counts.
				if bare == "PASS" || bare == "FAIL" {
					continue
				}
				if !userShort {
					if match := skipLine.FindStringSubmatch(strings.TrimPrefix(line, frameMarker)); match != nil {
						quiet[match[1]] = true
					}
				}
				kept.WriteString(line)
			}
			if verbose == "true" || verbose == "test2json" || runErr != nil {
				os.Stdout.Write(kept.Bytes())
			}
		}(i, part)
	}
	wg.Wait()

	code := 0
	ranQuiet := len(quiet) > 0
	if ranQuiet {
		var again []string
		for name := range quiet {
			again = append(again, regexp.QuoteMeta(name))
		}
		flag.Set("test.run", "^("+strings.Join(again, "|")+")$")
		code = m.Run()
	} else if !failed {
		fmt.Println("PASS")
	}
	if logPath != "" {
		if err := mergeTestLogs(logPath, ranQuiet, shardLogs); err != nil {
			// A log go test cannot read is not cached, which is the safe
			// failure; say why rather than leave it unexplained.
			fmt.Fprintf(os.Stderr, "ui shards: merging test logs: %v\n", err)
		}
	}
	if failed {
		// m.Run may have printed PASS for the quiet phase; the last verdict
		// line is the one a test2json reader keeps.
		fmt.Println("FAIL")
		return 1
	}
	return code
}

// listTests asks a child for the package's top-level tests. A child, so the
// listing sees exactly what a shard's -test.run will match against.
func listTests() ([]string, error) {
	cmd := exec.Command(os.Args[0], "-test.list=^Test")
	cmd.Env = append(os.Environ(), shardChildEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Test") {
			names = append(names, line)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no tests listed")
	}
	return names, nil
}

// needsQuietBox is for a test whose assertion is a wall-clock budget that
// holds on a quiet box and not against eight shards' worth of tmux traffic.
// It skips under -short, so a sharded run leaves it for the quiet phase and
// runs it there, alone and in full. Each caller passed 15 of 15 runs alone and
// failed in the shards.
func needsQuietBox(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("asserts a wall-clock budget; runs alone after the shards")
	}
}

// mergeTestLogs appends every shard's test log to the one go test handed this
// process, so the cache sees each golden, testdata file and variable a shard
// read. m.Run has written and closed the parent's log when the quiet phase
// ran; otherwise nothing has, and it is started here with the header go test
// checks for.
func mergeTestLogs(parent string, wroteParent bool, shards []string) error {
	const header = "# test log\n"
	var body strings.Builder
	if !wroteParent {
		body.WriteString(header)
	}
	// Read by this process before any m.Run was logging.
	body.WriteString("getenv " + shardChildEnv + "\n")
	body.WriteString("getenv " + shardCountEnv + "\n")
	for _, path := range shards {
		data, err := os.ReadFile(path)
		if err != nil {
			// A shard that died before m.Run wrote no log; its failure
			// already keeps this run out of the cache.
			continue
		}
		body.WriteString(strings.TrimPrefix(string(data), header))
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if !wroteParent {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(parent, flags, 0o666)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(body.String()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
