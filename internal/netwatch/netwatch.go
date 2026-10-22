// Package netwatch tells the manager whether the machine can reach the
// agents' APIs, so a send made while the wifi is down waits in the outbox
// instead of being typed into a session that cannot act on it.
//
// A prompt typed into an agent with no connection is not lost outright, but
// the turn it starts fails on its first request and the session lands on an
// error the operator then has to read, dismiss and resend around. Holding the
// send until the connection is back costs nothing: the message is already
// durable in the store, and the poller types it the pass after the watcher
// sees the network return.
package netwatch

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// DefaultTargets are the endpoints a probe tries. Any HTTP answer from any
// of them, an error status included, is a connection: the probe asks whether
// the network carries a request, not whether the API accepts this one.
var DefaultTargets = []string{
	"https://api.anthropic.com",
	"https://api.openai.com",
}

const (
	// OnlineEvery is the probe cadence while connected. A drop is noticed
	// within one period plus the probes it takes to confirm it.
	OnlineEvery = 15 * time.Second
	// OfflineEvery is the cadence while disconnected, shorter so the outbox
	// drains soon after the connection returns.
	OfflineEvery = 3 * time.Second
	// ProbeTimeout bounds one probe of one target.
	ProbeTimeout = 4 * time.Second
	// FailuresToOffline is how many failed probes in a row it takes to call
	// the machine offline. One timeout on a slow network is not an outage,
	// and holding every send over it would be.
	FailuresToOffline = 2
)

// Probe reports whether the network is reachable right now.
type Probe func(ctx context.Context) bool

// HTTPProbe is a Probe that succeeds when any target answers an HTTP HEAD.
// It goes through http.ProxyFromEnvironment, as the agents do, so a machine
// that only reaches the internet through a proxy is not read as offline.
func HTTPProbe(targets []string, timeout time.Duration) Probe {
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return func(ctx context.Context) bool {
		for _, target := range targets {
			req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
			if err != nil {
				continue
			}
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			resp.Body.Close()
			return true
		}
		return false
	}
}

// Watcher holds the latest connectivity reading. Its zero value, and any
// watcher that has not finished a probe, reads online: an outbox that holds
// everything until a first probe lands would delay every send at startup
// for a network that is almost always there.
type Watcher struct {
	probe Probe

	mu       sync.Mutex
	offline  bool
	since    time.Time
	failures int
}

// New returns a watcher over probe. A nil probe never reports offline.
func New(probe Probe) *Watcher {
	return &Watcher{probe: probe}
}

// Offline reports whether the last probes found no network, and since when.
func (w *Watcher) Offline() (bool, time.Time) {
	if w == nil {
		return false, time.Time{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.offline, w.since
}

// Check runs one probe and records it, reporting whether the machine came
// back online with this probe.
func (w *Watcher) Check(ctx context.Context, now time.Time) (restored bool) {
	if w == nil || w.probe == nil {
		return false
	}
	ok := w.probe(ctx)
	return w.record(ok, now)
}

func (w *Watcher) record(ok bool, now time.Time) (restored bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if ok {
		restored = w.offline
		w.offline, w.since, w.failures = false, time.Time{}, 0
		return restored
	}
	w.failures++
	if !w.offline && w.failures >= FailuresToOffline {
		w.offline, w.since = true, now
	}
	return false
}

// Run probes until ctx ends, calling onRestore each time the network comes
// back so the caller can deliver what it held without waiting for its own
// next tick.
func (w *Watcher) Run(ctx context.Context, onRestore func()) {
	if w == nil || w.probe == nil {
		return
	}
	for {
		probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
		restored := w.Check(probeCtx, time.Now())
		cancel()
		if restored && onRestore != nil {
			onRestore()
		}
		every := OnlineEvery
		if offline, _ := w.Offline(); offline || w.failing() {
			every = OfflineEvery
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// failing reports a failed probe not yet confirmed as an outage, so the
// confirming probe runs at the offline cadence rather than a full period
// later.
func (w *Watcher) failing() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failures > 0
}
