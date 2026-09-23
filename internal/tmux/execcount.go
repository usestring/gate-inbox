package tmux

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Every tmux command the manager runs is a forked process, and what the board
// costs is the rate of those rather than the price of any one of them. A
// capture is about three milliseconds, which is affordable at the poll's
// cadence and is not affordable eighty times a second; the difference between
// those two is a cadence decision made in the UI, several packages away from
// the fork it turns into.
//
// Nothing here bounds that rate. This counts it, so a budget test can assert
// the rate a scenario actually produces instead of asserting the guards that
// were supposed to produce it -- every one of those guards was tested on its
// own while the sum of them went unmeasured, which is how a cadence cut from
// 83 captures a second to 25 was found back at 54 on the operator's board.
//
// The counters are global rather than per-Driver because the thing worth
// bounding is what the process does to the machine, and a board that grew a
// second Driver would otherwise report half its own cost.
var (
	execTotal  atomic.Int64
	execByVerb sync.Map // string -> *atomic.Int64
)

// countExec records one forked tmux invocation, keyed by its subcommand.
//
// It runs on every tmux fork, so it stays two atomic increments and no
// allocation in the steady state: the map is written once per distinct verb
// and read only by whoever asks for the counts.
func countExec(full []string) {
	execTotal.Add(1)
	verb := execVerb(full)
	if counter, ok := execByVerb.Load(verb); ok {
		counter.(*atomic.Int64).Add(1)
		return
	}
	counter, _ := execByVerb.LoadOrStore(verb, new(atomic.Int64))
	counter.(*atomic.Int64).Add(1)
}

// execVerb is the tmux subcommand in an argument list that begins with the
// server flags the driver always supplies (-L <socket>, and -f for a config
// where one is passed). A list of chained commands is keyed by its first:
// what the count is for is telling capture-pane apart from send-keys, not
// itemising a command list that costs one fork however long it is.
func execVerb(full []string) string {
	for i := 0; i < len(full); i++ {
		arg := full[i]
		if len(arg) > 0 && arg[0] == '-' {
			// -L, -S and -f carry a value; the rest are bare switches. -S is
			// the socket path, and skipping it matters: without that, a call
			// that addresses a server by path is keyed by the path itself
			// rather than by what it asked the server to do.
			if arg == "-L" || arg == "-S" || arg == "-f" {
				i++
			}
			continue
		}
		return arg
	}
	return "(none)"
}

// ExecTotal is every tmux process this manager has forked.
func ExecTotal() int64 { return execTotal.Load() }

// ExecCounts is the fork count per subcommand, for a caller reporting where
// the board's process budget went.
func ExecCounts() map[string]int64 {
	counts := map[string]int64{}
	execByVerb.Range(func(key, value any) bool {
		counts[key.(string)] = value.(*atomic.Int64).Load()
		return true
	})
	return counts
}

// ExecVerbs is ExecCounts' keys, busiest first, for a report that shows the
// few verbs that matter rather than every one ever run.
func ExecVerbs() []string {
	counts := ExecCounts()
	verbs := make([]string, 0, len(counts))
	for verb := range counts {
		verbs = append(verbs, verb)
	}
	sort.Slice(verbs, func(i, j int) bool {
		if counts[verbs[i]] != counts[verbs[j]] {
			return counts[verbs[i]] > counts[verbs[j]]
		}
		return verbs[i] < verbs[j]
	})
	return verbs
}

// ResetExecCounts zeroes the counters. It is for a budget test measuring one
// scenario, which needs the count of that scenario rather than of everything
// the process has done since it started.
func ResetExecCounts() {
	execTotal.Store(0)
	execByVerb.Range(func(_, value any) bool {
		value.(*atomic.Int64).Store(0)
		return true
	})
}
