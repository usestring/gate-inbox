# Codex usage-limit recovery performance

Measured on 2026-09-06, Linux/amd64, Intel i9-9900K, Go 1.26.5, `GOMAXPROCS=2`.
The comparison uses the exact pre-feature revision `a84395afe5b25ce0ee7ea993065bbcf2bcbf015b`
and the Codex-only implementation. Claude sessions use native continuation and receive
no manager recovery records or prompts.

## Steady polling

Median milliseconds per fleet pass from three alternating baseline/feature runs, 500 ms
minimum per case. Each fleet is half Claude and half Codex, with identical 40-line
ANSI-coloured pane histories. Only Codex rows carry recovery state in the feature build.
Session and inbox reads, status derivation, and recovery reads/checks are timed. Setup,
tmux capture, process sampling, preview rendering, and sending prompts are excluded.
The SQLite fixture uses the existing memory-backed test scratch directory; these are
warm read costs, not disk-latency bounds.

| Sessions | State | Pre-feature | Codex-only recovery |
| ---: | --- | ---: | ---: |
| 10 | Idle | 3.74 | 3.97 |
| 10 | Waiting for reset | 1.11 | 1.24 |
| 10 | Already reprompted | 1.03 | 1.30 |
| 100 | Idle | 34.24 | 37.33 |
| 100 | Waiting for reset | 10.40 | 12.51 |
| 100 | Already reprompted | 9.60 | 13.17 |
| 1,000 | Idle | 369.13 | 372.20 |
| 1,000 | Waiting for reset | 99.15 | 112.45 |
| 1,000 | Already reprompted | 95.33 | 113.68 |

The 100-session waiting case adds 2.11 ms per pass; the 1,000-session case adds 13.30 ms.
The 100-session idle case is 3.09 ms slower, while the 1,000-session idle difference is
3.07 ms. Three samples on a shared host do not establish statistical significance.

## Simultaneous resets

`BenchmarkLimitRecoveryBurst` runs the full poll pass against isolated shell panes using
Codex status/prompt rules. It includes capture, process sampling, scheduling, durable
claims, paste, and Enter. Every run checks that every fixture was claimed and echoed the
continuation. No live agent or model request participates.

Automatic continuation is capped at ten sends per pass. In the original uncapped
implementation, a 100-session confirmation run held one pass for 2.27 seconds.

| Codex sessions due together | Passes | Median total active polling | Longest individual pass in each of three runs |
| ---: | ---: | ---: | --- |
| 10 | 1 | 190 ms | 213 / 190 / 173 ms |
| 100 | 10 | 2.34 s | 255 / 267 / 1,673 ms |

The third 100-session run took 3.68 seconds of active polling and included a 1.67-second
pass. The count cap held in every run, but synchronous delivery does not impose a
wall-clock deadline. Slow echoes or host stalls can still lengthen a batch.

The benchmark drains passes consecutively without sleeping. At the normal two-second
cadence, 100 due sessions require ten polling opportunities (roughly eighteen seconds
plus the final pass when passes fit the interval). A regression test also verifies that
the eleventh overdue session waits for the next pass. The 1,000-session test covers
steady polling, not simultaneous real-CLI deliveries.

## Claim writes

The unchanged disk-backed WAL claim path was measured earlier in this session at
0.102 ms per claim (median of five 100-claim runs; range 0.094–0.132 ms), about 1.82 KB
and 30 allocations per claim. This times updates to existing records; database creation,
initial scheduling, checkpointing, and cold-disk behaviour are excluded. The full suite
took 74.8 seconds, so warm claim latency is not an fsync latency guarantee.

## Reproduce

From `gate-inbox`, using existing Go dependencies and tmux:

```sh
GOMAXPROCS=2 go test ./internal/ui -run '^$' -bench '^BenchmarkLimitPollFleet$' -benchtime=500ms -count=3 -benchmem
GOMAXPROCS=2 go test ./internal/ui -run '^$' -bench '^BenchmarkLimitRecoveryBurst$' -benchtime=1x -count=3 -benchmem
GOMAXPROCS=2 go test ./internal/store -run '^$' -bench '^BenchmarkLimitRecoveryClaim$' -benchtime=100x -count=5 -benchmem
```

For a base comparison, export the pre-feature revision into a temporary directory with
`git archive`, copy only `internal/ui/limitrecovery_bench_test.go` into its corresponding
package, and add the adapter below as `internal/ui/limitrecovery_bench_adapter_test.go`.
Compile each revision once with `go test -c ./internal/ui`, then run the two binaries
alternately with the same benchmark flags. The adapter removes only recovery work that
did not exist on the base; fixture construction and status derivation are unchanged.

```go
package ui

import (
    "time"
    "github.com/usestring/gate-inbox/internal/status"
    "github.com/usestring/gate-inbox/internal/store"
)

func seedLimitBenchmark(st *store.Store, engine *status.Engine, sess store.Session, pane string, now, attempted time.Time) error {
    return nil
}

func benchmarkLimitPoll(p *poller, sessions []store.Session, panes map[string]string, now time.Time) error {
    hashes := make(map[string]uint64, len(sessions))
    for _, sess := range sessions {
        if _, err := p.derivePaneStatus(sess, panes[sess.ID], true, hashes); err != nil {
            return err
        }
    }
    p.paneHashes = hashes
    return nil
}
```
