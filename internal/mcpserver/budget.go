package mcpserver

import (
	"context"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every agent session runs its own MCP server, so a box with a few hundred
// sessions runs a few hundred copies, nearly all of them idle. The Go defaults
// size each one for a busy many-core service: a P and an OS thread per core,
// a heap allowed to double before collecting, and pages left mapped after the
// startup burst (schema inference for the tool set allocates ~11 MB to keep
// ~2 MB). These defaults bound each copy to what one session needs. An
// operator's GOMAXPROCS or GOGC in the environment still wins.
//
// The GC target is 50, not lower: measured over 180 servers starting at
// once, 25 held no less memory than 50 but cost the startup burst ~15% more
// CPU and slower calls, and 75 held ~110 MB more for no further gain.
const (
	budgetProcs     = 2
	budgetGCPercent = 50
	// releaseAfter is how long the server sits without a request before it
	// hands the pages a burst touched back to the OS.
	releaseAfter = 5 * time.Second
)

// applyBudget sets the server's runtime limits. It runs before the server is
// built, so the startup burst already collects at the lower GC target.
func applyBudget() {
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(budgetProcs)
	}
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(budgetGCPercent)
	}
	// Nothing reads a heap profile from an MCP server; sampling would only
	// grow the profiler's buckets.
	runtime.MemProfileRate = 0
}

// releaser returns freed heap pages to the OS once requests stop. Go's
// background scavenger does this too, but too slowly and too little: an idle
// server measured hours after its last request still held ~4x its live heap.
type releaser struct {
	mu      sync.Mutex
	timer   *time.Timer
	after   time.Duration
	free    func()
	stopped bool
}

func newReleaser(after time.Duration, free func()) *releaser {
	r := &releaser{after: after, free: free}
	r.timer = time.AfterFunc(after, free)
	return r
}

// touch postpones the release until the server has been quiet for the whole
// interval, so a burst of calls pays for one collection, after it ends.
func (r *releaser) touch() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.stopped {
		r.timer.Reset(r.after)
	}
}

// stop is final: a request that finishes while the server shuts down does
// not re-arm the release.
func (r *releaser) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	r.timer.Stop()
}

// middleware re-arms the release after each request has been answered.
func (r *releaser) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		r.touch()
		return result, err
	}
}
