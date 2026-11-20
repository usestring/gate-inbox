package extensionhost

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

func TestInfoCarriesTheSessionTimes(t *testing.T) {
	created := time.Unix(1_700_000_000, 0)
	archived := created.Add(time.Hour)
	got := info(sessioncmd.Session{ID: "a1", Archived: true, CreatedAt: created, ArchivedAt: archived})
	if !got.CreatedAt.Equal(created) || !got.ArchivedAt.Equal(archived) {
		t.Fatalf("times = %v, %v; want %v, %v", got.CreatedAt, got.ArchivedAt, created, archived)
	}
	if got := info(sessioncmd.Session{ID: "a2", CreatedAt: created}); !got.ArchivedAt.IsZero() {
		t.Fatalf("an unarchived session has ArchivedAt %v", got.ArchivedAt)
	}
	launched := created.Add(2 * time.Hour)
	if got := info(sessioncmd.Session{ID: "a3", CreatedAt: created, LaunchedAt: launched}); !got.LaunchedAt.Equal(launched) {
		t.Fatalf("LaunchedAt = %v, want %v", got.LaunchedAt, launched)
	}
}
