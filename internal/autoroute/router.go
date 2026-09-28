package autoroute

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"
)

const cacheTTL = 2 * time.Minute

var ErrNoQuota = errors.New("no enabled CLI has fresh usable subscription quota")

type Window struct {
	Used     float64
	ResetsAt time.Time
	Duration time.Duration
}

type Reading struct {
	ObservedAt time.Time
	Windows    []Window
}

type ReadFunc func(context.Context, string) (Reading, error)

type Router struct {
	read  ReadFunc
	mu    sync.Mutex
	cache map[string]Reading
}

func New(read ReadFunc) *Router {
	return &Router{read: read, cache: make(map[string]Reading)}
}

func (r *Router) reading(ctx context.Context, name string, now, started time.Time) (Reading, error) {
	r.mu.Lock()
	cached, ok := r.cache[name]
	r.mu.Unlock()
	current := now.Add(time.Since(started))
	if ok && fresh(cached, current) && windowsCurrent(cached, current) {
		return cached, nil
	}
	reading, err := r.read(ctx, name)
	current = now.Add(time.Since(started))
	if err != nil || !fresh(reading, current) || !windowsCurrent(reading, current) {
		return Reading{}, ErrNoQuota
	}
	r.mu.Lock()
	r.cache[name] = reading
	r.mu.Unlock()
	return reading, nil
}

func windowsCurrent(reading Reading, now time.Time) bool {
	if len(reading.Windows) == 0 {
		return false
	}
	for _, window := range reading.Windows {
		if !window.ResetsAt.After(now) {
			return false
		}
	}
	return true
}

func fresh(reading Reading, now time.Time) bool {
	return !reading.ObservedAt.IsZero() && !reading.ObservedAt.After(now.Add(5*time.Second)) && now.Sub(reading.ObservedAt) <= cacheTTL
}

// Score uses the most constrained window. A new session reserves quota for
// work already running, then prefers the CLI furthest behind an even pace.
func Score(reading Reading, active int, now time.Time) (float64, bool) {
	if !fresh(reading, now) || len(reading.Windows) == 0 {
		return 0, false
	}
	reserve := 10 + 5*active
	if reserve > 50 {
		reserve = 50
	}
	score := math.Inf(-1)
	for _, window := range reading.Windows {
		if window.Duration <= 0 || !window.ResetsAt.After(now) || window.ResetsAt.After(now.Add(window.Duration+5*time.Second)) || math.IsNaN(window.Used) || window.Used < 0 || window.Used > 100 {
			return 0, false
		}
		committed := window.Used + float64(reserve)
		if committed >= 100 {
			return 0, false
		}
		elapsed := math.Max(0, 1-window.ResetsAt.Sub(now).Seconds()/window.Duration.Seconds())
		score = math.Max(score, committed-100*elapsed)
	}
	return score, true
}

func (r *Router) Choose(ctx context.Context, names []string, active map[string]int, now time.Time) (string, error) {
	started := time.Now()
	type result struct {
		name    string
		reading Reading
	}
	results := make(chan result, len(names))
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Go(func() {
			reading, err := r.reading(ctx, name, now, started)
			if err == nil {
				results <- result{name, reading}
			}
		})
	}
	wg.Wait()
	close(results)
	now = now.Add(time.Since(started))
	readings := make(map[string]Reading, len(names))
	for result := range results {
		readings[result.name] = result.reading
	}
	best, bestScore := "", math.Inf(1)
	for _, name := range names {
		reading, ok := readings[name]
		if !ok {
			continue
		}
		score, ok := Score(reading, active[name], now)
		if ok && score < bestScore {
			best, bestScore = name, score
		}
	}
	if best == "" {
		return "", ErrNoQuota
	}
	return best, nil
}
