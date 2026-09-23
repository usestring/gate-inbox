package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// The countdown is the one thing about an archived row that cannot be read
// off the row itself, so it has to be right at both ends of the window.
func TestArchiveTimeLeftCountsDown(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		sess store.Session
		want string
	}{
		{"live row is not on the clock", store.Session{}, ""},
		{"unstamped archive is not on the clock", store.Session{Archived: true}, ""},
		{"just filed", store.Session{Archived: true, ArchivedAt: now}, "7d left"},
		{"most of the way through", store.Session{Archived: true, ArchivedAt: now.Add(-6 * 24 * time.Hour)}, "24h left"},
		{"last hours", store.Session{Archived: true, ArchivedAt: now.Add(-archiveRetention + 90*time.Minute)}, "2h left"},
		{"last minutes", store.Session{Archived: true, ArchivedAt: now.Add(-archiveRetention + 30*time.Second)}, "1m left"},
		{"past the window", store.Session{Archived: true, ArchivedAt: now.Add(-archiveRetention - time.Hour)}, "due to go"},
	}
	for _, tc := range cases {
		if got := archiveTimeLeft(tc.sess); got != tc.want {
			t.Errorf("%s: archiveTimeLeft = %q, want %q", tc.name, got, tc.want)
		}
	}
}
