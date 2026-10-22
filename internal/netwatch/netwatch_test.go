package netwatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func probeOf(results ...bool) Probe {
	return func(context.Context) bool {
		ok := results[0]
		if len(results) > 1 {
			results = results[1:]
		}
		return ok
	}
}

func TestAWatcherThatHasNotProbedReadsOnline(t *testing.T) {
	if offline, _ := New(probeOf(false)).Offline(); offline {
		t.Fatal("a fresh watcher read offline before any probe")
	}
	var nilWatcher *Watcher
	if offline, _ := nilWatcher.Offline(); offline {
		t.Fatal("a nil watcher read offline")
	}
}

func TestOneFailedProbeIsNotAnOutage(t *testing.T) {
	w := New(probeOf(false, false))
	now := time.Unix(1000, 0)
	w.Check(context.Background(), now)
	if offline, _ := w.Offline(); offline {
		t.Fatal("one failed probe read offline")
	}
	w.Check(context.Background(), now.Add(time.Second))
	offline, since := w.Offline()
	if !offline || !since.Equal(now.Add(time.Second)) {
		t.Fatalf("offline=%v since=%v, want offline since the confirming probe", offline, since)
	}
}

func TestASuccessfulProbeRestoresTheConnectionAndSaysSo(t *testing.T) {
	w := New(probeOf(false, false, true, true))
	now := time.Now()
	w.Check(context.Background(), now)
	w.Check(context.Background(), now)
	if !w.Check(context.Background(), now) {
		t.Fatal("the probe that ended the outage did not report a restore")
	}
	if offline, _ := w.Offline(); offline {
		t.Fatal("still offline after a successful probe")
	}
	if w.Check(context.Background(), now) {
		t.Fatal("a probe while already online reported a restore")
	}
}

func TestAFailureBetweenSuccessesStartsTheCountAgain(t *testing.T) {
	w := New(probeOf(false, true, false))
	now := time.Now()
	for range 3 {
		w.Check(context.Background(), now)
	}
	if offline, _ := w.Offline(); offline {
		t.Fatal("two failures separated by a success read offline")
	}
}

func TestHTTPProbeCountsAnErrorStatusAsConnected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	if !HTTPProbe([]string{server.URL}, time.Second)(context.Background()) {
		t.Fatal("a 401 read as no connection")
	}
}

func TestHTTPProbeTriesEveryTargetBeforeGivingUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead := server.URL
	server.Close()
	live := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer live.Close()
	if HTTPProbe([]string{dead}, time.Second)(context.Background()) {
		t.Fatal("a closed server read as connected")
	}
	if !HTTPProbe([]string{dead, live.URL}, time.Second)(context.Background()) {
		t.Fatal("a live second target was not tried")
	}
}
