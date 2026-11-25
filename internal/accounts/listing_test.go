package accounts

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
)

// pooled lists secrets named ACCT_<account>.
var pooled = config.Tool{AccountSecret: "ACCT_{account}", AccountsCommand: "list"}

// cachedStub replaces run with fn for ListCached and starts each test on an
// empty cache, so one test's listing is never another's.
func cachedStub(t *testing.T, fn func(command string) (string, error)) {
	t.Helper()
	resetListings()
	stub(t, fn)
	t.Cleanup(resetListings)
}

func resetListings() {
	listings.Lock()
	defer listings.Unlock()
	listings.byCommand = map[string]*listing{}
}

// waitListed waits for the command's background read to end.
func waitListed(t *testing.T, command string) {
	t.Helper()
	listings.Lock()
	entry := listings.byCommand[command]
	var done chan struct{}
	if entry != nil {
		done = entry.done
	}
	listings.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the background listing never finished")
	}
}

// ageListing makes the cached listing look older than listingFresh.
func ageListing(command string) {
	listings.Lock()
	defer listings.Unlock()
	listings.byCommand[command].at = time.Now().Add(-2 * listingFresh)
}

// The account card opens on a key press, so it must not wait on a secret
// store that takes seconds: once a listing is in hand, later calls answer
// from it, and a stale one is served while a refresh runs behind it.
func TestListCachedAnswersFromTheLastListingAndRefreshesBehindIt(t *testing.T) {
	var calls atomic.Int32
	out := atomic.Value{}
	out.Store("ACCT_ALICE1\n")
	cachedStub(t, func(string) (string, error) {
		calls.Add(1)
		return out.Load().(string), nil
	})

	if names, err := ListCached(pooled); err != nil || !slices.Equal(names, []string{"ALICE1"}) {
		t.Fatalf("first listing = %v, %v", names, err)
	}
	if names, _ := ListCached(pooled); !slices.Equal(names, []string{"ALICE1"}) || calls.Load() != 1 {
		t.Fatalf("a fresh listing was read again: %v after %d reads", names, calls.Load())
	}

	out.Store("ACCT_ALICE1\nACCT_BOB2\n")
	ageListing(pooled.AccountsCommand)
	if names, _ := ListCached(pooled); !slices.Equal(names, []string{"ALICE1"}) {
		t.Fatalf("a stale listing should still answer at once, got %v", names)
	}
	waitListed(t, pooled.AccountsCommand)
	if names, _ := ListCached(pooled); !slices.Equal(names, []string{"ALICE1", "BOB2"}) || calls.Load() != 2 {
		t.Fatalf("the refresh did not land: %v after %d reads", names, calls.Load())
	}
}

// Prefetch is the board reading ahead of the card, so the card's first call
// waits on that read rather than starting a second one.
func TestPrefetchIsTheReadTheCardWaitsOn(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	cachedStub(t, func(string) (string, error) {
		calls.Add(1)
		<-release
		return "ACCT_ALICE1\n", nil
	})

	Prefetch(pooled)
	Prefetch(pooled)
	got := make(chan []string)
	go func() {
		names, _ := ListCached(pooled)
		got <- names
	}()
	close(release)
	if names := <-got; !slices.Equal(names, []string{"ALICE1"}) || calls.Load() != 1 {
		t.Fatalf("names %v after %d reads, want one read", names, calls.Load())
	}
}

// A failed refresh keeps the last good listing, and a command that has
// never listed reports its error and is read again next time.
func TestListCachedKeepsTheLastGoodListingThroughAFailure(t *testing.T) {
	fail := atomic.Bool{}
	cachedStub(t, func(string) (string, error) {
		if fail.Load() {
			return "", errors.New("no answer within 30s")
		}
		return "ACCT_ALICE1\n", nil
	})

	fail.Store(true)
	if _, err := ListCached(pooled); err == nil {
		t.Fatal("a failed first listing reported no error")
	}
	fail.Store(false)
	if names, err := ListCached(pooled); err != nil || !slices.Equal(names, []string{"ALICE1"}) {
		t.Fatalf("a failed listing was not retried: %v, %v", names, err)
	}

	fail.Store(true)
	ageListing(pooled.AccountsCommand)
	ListCached(pooled)
	waitListed(t, pooled.AccountsCommand)
	if names, err := ListCached(pooled); err != nil || !slices.Equal(names, []string{"ALICE1"}) {
		t.Fatalf("a failed refresh lost the last listing: %v, %v", names, err)
	}
}
