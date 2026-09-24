package extensionhost

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/store"
)

func within(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestBoardDeliversTransitionsInOrderWithTheirKind(t *testing.T) {
	board := NewEvents(NewBoard("/config", nil), nil)
	host, release := board.For("ext")
	defer release()
	if host.ConfigDir() != "/config" {
		t.Fatalf("ConfigDir = %q", host.ConfigDir())
	}
	var got []extension.StatusEvent
	done := make(chan struct{})
	host.Subscribe(func(e extension.StatusEvent) {
		got = append(got, e)
		if len(got) == 3 {
			close(done)
		}
	})
	at := time.Unix(100, 0)
	board.Transition("a", "working", "finished", at)
	board.Transition("a", "finished", "waiting", at)
	board.Transition("b", "working", "idle", at)
	within(t, "three events", done)
	want := []extension.StatusEvent{
		{SessionID: "a", From: "working", To: "finished", Kind: extension.EventStop, At: at},
		{SessionID: "a", From: "finished", To: "waiting", Kind: extension.EventAsk, At: at},
		{SessionID: "b", From: "working", To: "idle", At: at},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBoardContainsAPanickingSubscriber(t *testing.T) {
	var mu sync.Mutex
	var reported []string
	board := NewEvents(NewBoard("", nil), func(owner string, err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, owner+": "+err.Error())
	})
	bad, releaseBad := board.For("bad")
	defer releaseBad()
	good, releaseGood := board.For("good")
	defer releaseGood()
	bad.Subscribe(func(extension.StatusEvent) { panic("boom") })
	done := make(chan struct{}, 2)
	good.Subscribe(func(extension.StatusEvent) { done <- struct{}{} })

	board.Transition("a", "working", "errored", time.Now())
	board.Transition("a", "errored", "working", time.Now())
	within(t, "the first event", done)
	within(t, "the second event", done)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(reported)
		mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 2 || !strings.Contains(reported[0], "bad: a board subscriber panicked: boom") {
		t.Fatalf("reported = %q, want each panic reported against its extension, and the subscriber kept", reported)
	}
}

func TestBoardReleaseEndsEverySubscriptionOfThatExtension(t *testing.T) {
	board := NewEvents(NewBoard("", nil), nil)
	host, release := board.For("ext")
	other, releaseOther := board.For("other")
	defer releaseOther()
	calls := make(chan string, 16)
	host.Subscribe(func(extension.StatusEvent) { calls <- "event" })
	host.OnPass(func(extension.Pass) { calls <- "pass" })
	seen := make(chan struct{}, 1)
	other.Subscribe(func(extension.StatusEvent) { seen <- struct{}{} })

	release()
	// A goroutine the extension left behind cannot subscribe again.
	host.Subscribe(func(extension.StatusEvent) { calls <- "late" })
	board.Transition("a", "working", "finished", time.Now())
	board.Pass(time.Now(), []store.Session{{ID: "a", Status: "finished"}})
	within(t, "the other extension's event", seen)
	select {
	case call := <-calls:
		t.Fatalf("a released extension was still called: %s", call)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBoardASlowSubscriberDelaysOnlyItself(t *testing.T) {
	board := NewEvents(NewBoard("", nil), nil)
	slow, releaseSlow := board.For("slow")
	fast, releaseFast := board.For("fast")
	unblock := make(chan struct{})
	defer func() {
		close(unblock)
		releaseSlow()
		releaseFast()
	}()
	slow.Subscribe(func(extension.StatusEvent) { <-unblock })
	done := make(chan struct{})
	count := 0
	fast.Subscribe(func(extension.StatusEvent) {
		if count++; count == 100 {
			close(done)
		}
	})
	returned := make(chan struct{})
	go func() {
		for range 100 {
			board.Transition("a", "working", "finished", time.Now())
		}
		close(returned)
	}()
	within(t, "the poll side to return", returned)
	within(t, "the fast subscriber", done)
}

func TestBoardPassesSkipArchivedAndCoalesceToTheNewest(t *testing.T) {
	board := NewEvents(NewBoard("", nil), nil)
	host, release := board.For("ext")
	defer release()
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var mu sync.Mutex
	var got []extension.Pass
	first := true
	done := make(chan struct{})
	host.OnPass(func(p extension.Pass) {
		if first {
			first = false
			close(entered)
			<-unblock
		}
		mu.Lock()
		got = append(got, p)
		n := len(got)
		mu.Unlock()
		if n == 2 {
			close(done)
		}
	})
	board.Pass(time.Unix(1, 0), []store.Session{{ID: "a", Status: "working"}, {ID: "z", Status: "idle", Archived: true}})
	within(t, "the first pass", entered)
	board.Pass(time.Unix(2, 0), nil)
	board.Pass(time.Unix(3, 0), []store.Session{{ID: "a", Status: "finished"}})
	close(unblock)
	within(t, "the newest pass", done)
	mu.Lock()
	defer mu.Unlock()
	if len(got[0].Sessions) != 1 || got[0].Sessions[0] != (extension.SessionStatus{ID: "a", Status: "working"}) {
		t.Fatalf("first pass = %+v, want the archived row left out", got[0])
	}
	if !got[1].At.Equal(time.Unix(3, 0)) || got[1].Sessions[0].Status != "finished" {
		t.Fatalf("second delivery = %+v, want only the newest pass", got[1])
	}
}

func TestBoardDropsTheOldestEventsOfAStuckSubscriber(t *testing.T) {
	reported := make(chan error, 1)
	board := NewEvents(NewBoard("", nil), func(_ string, err error) {
		select {
		case reported <- err:
		default:
		}
	})
	host, release := board.For("ext")
	defer release()
	entered := make(chan struct{})
	unblock := make(chan struct{})
	held := false
	host.Subscribe(func(extension.StatusEvent) {
		if !held {
			held = true
			close(entered)
			<-unblock
		}
	})
	board.Transition("held", "working", "finished", time.Now())
	within(t, "the held event", entered)
	for range maxQueued + 3 {
		board.Transition("a", "working", "finished", time.Now())
	}
	close(unblock)
	select {
	case err := <-reported:
		if !strings.Contains(err.Error(), "3 event(s) dropped") {
			t.Fatalf("reported %v, want 3 dropped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the dropped events were not reported")
	}
}
