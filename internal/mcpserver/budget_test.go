package mcpserver

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The releaser's timing runs on synctest's fake clock, so these hold on any
// runner speed.

const interval = 5 * time.Second

func TestReleaserRunsOnceAfterQuietStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freed := 0
		r := newReleaser(interval, func() { freed++ })
		defer r.stop()
		time.Sleep(10 * interval)
		synctest.Wait()
		if freed != 1 {
			t.Fatalf("freed %d times after a quiet start, want 1", freed)
		}
	})
}

func TestReleaserWaitsForBurstToEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freed := 0
		r := newReleaser(interval, func() { freed++ })
		defer r.stop()
		handler := r.middleware(func(context.Context, string, mcp.Request) (mcp.Result, error) {
			return nil, nil
		})
		// Requests closer together than the interval keep postponing it,
		// past the point the first release would otherwise have fired.
		for range 5 {
			time.Sleep(interval / 2)
			if _, err := handler(context.Background(), "tools/call", nil); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if freed != 0 {
			t.Fatalf("freed %d times during a burst, want 0", freed)
		}
		time.Sleep(interval)
		synctest.Wait()
		if freed != 1 {
			t.Fatalf("freed %d times after the burst, want 1", freed)
		}
	})
}

func TestReleaserStopIsFinal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freed := 0
		r := newReleaser(interval, func() { freed++ })
		r.stop()
		r.touch()
		time.Sleep(10 * interval)
		synctest.Wait()
		if freed != 0 {
			t.Fatalf("freed %d times after stop, want 0", freed)
		}
	})
}
