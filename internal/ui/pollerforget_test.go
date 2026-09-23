package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// TestPollerForgetsVanishedSessions is the poller's half of the same leak the
// worktracker had: state keyed by session id that nothing ever visits again
// once the session is gone.
//
// quietSince and operatorInputAt are the two maps in the poller that are not
// rebuilt wholesale on each pass. A session that ended while quiet, or that
// was clicked once and then killed, used to leave an entry behind for the life
// of the process. 500 ended sessions here is a day or two of a real board; the
// assertion is that what survives is the live set, not a fraction of the
// churned one.
func TestPollerForgetsVanishedSessions(t *testing.T) {
	p := &poller{
		quietSince:      map[string]time.Time{},
		operatorInputAt: map[string]time.Time{},
	}

	const ended = 500
	for i := 0; i < ended; i++ {
		id := fmt.Sprintf("gone-%d", i)
		p.quietSince[id] = time.Now()
		p.operatorInputAt[id] = time.Now()
	}

	live := []store.Session{{ID: "still-here"}, {ID: "also-here"}}
	for _, sess := range live {
		p.quietSince[sess.ID] = time.Now()
		p.operatorInputAt[sess.ID] = time.Now()
	}

	p.forgetVanished(live)

	if got := len(p.quietSince); got != len(live) {
		t.Errorf("quietSince holds %d entries after %d sessions ended; want %d", got, ended, len(live))
	}
	if got := len(p.operatorInputAt); got != len(live) {
		t.Errorf("operatorInputAt holds %d entries after %d sessions ended; want %d", got, ended, len(live))
	}
	// Forgetting the dead must not forget the living: these are a debounce
	// stamp and an echo window, and dropping them mid-session flashes the row.
	for _, sess := range live {
		if _, ok := p.quietSince[sess.ID]; !ok {
			t.Errorf("quietSince dropped live session %q", sess.ID)
		}
		if _, ok := p.operatorInputAt[sess.ID]; !ok {
			t.Errorf("operatorInputAt dropped live session %q", sess.ID)
		}
	}
}
