package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// Replaying captures isolates polling overhead from tmux/process sampling.
// The adapter is the only difference when replaying the pre-feature revision.
func BenchmarkLimitPollFleet(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		for _, workload := range []string{"idle", "waiting", "claimed"} {
			b.Run(fmt.Sprintf("%d/%s", size, workload), func(b *testing.B) {
				cfg, err := config.Default()
				if err != nil {
					b.Fatal(err)
				}
				engine, err := status.NewEngine(cfg)
				if err != nil {
					b.Fatal(err)
				}
				st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(b), "bench.db"))
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { st.Close() })
				p := newPoller(st, nil, engine, nil, nil, nil, nil, nil, nil, time.Second)
				now := time.Date(2026, 9, 6, 20, 30, 0, 0, time.UTC)
				panes := make(map[string]string, size)
				for i := range size {
					tool, prompt := "claude", "❯ "
					if i%2 != 0 {
						tool, prompt = "codex", "› "
					}
					current, tail := status.Idle, "Completed the requested changes."
					if workload != "idle" {
						current, tail = status.Errored, "You've hit your usage limit. Try again at Sep 7th, 2026 9:00 PM."
					}
					id := fmt.Sprintf("worker-%04d", i)
					if err := st.CreateSession(store.Session{ID: id, Name: id, Tool: tool, Cwd: "/tmp", Status: current}); err != nil {
						b.Fatal(err)
					}
					panes[id] = strings.Repeat("\x1b[32mRead source file and checked the previous tool result.\x1b[0m\n", 40) + tail + "\n\n" + prompt
				}
				sessions, err := st.ListSessions(false)
				if err != nil {
					b.Fatal(err)
				}
				// Seed the durable states exactly as a prior poll would, without
				// sending prompts or contacting an agent during the benchmark.
				for _, sess := range sessions {
					if workload == "idle" {
						continue
					}
					attempt := time.Time{}
					if workload == "claimed" {
						attempt = now
					}
					if err := seedLimitBenchmark(st, engine, sess, panes[sess.ID], now, attempt); err != nil {
						b.Fatal(err)
					}
				}
				if err := benchmarkLimitPoll(p, sessions, panes, now); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					sessions, err := st.ListSessions(false)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := st.HeadMessages(); err != nil {
						b.Fatal(err)
					}
					if err := benchmarkLimitPoll(p, sessions, panes, now); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
