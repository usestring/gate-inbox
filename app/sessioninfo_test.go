package app

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// A filter's Keep is told when the row was created and archived.
func TestSessionInfoCarriesTheRowTimes(t *testing.T) {
	created := time.Unix(1_700_000_000, 0)
	archived := created.Add(time.Hour)
	got := sessionInfo(store.Session{ID: "a1", Archived: true, CreatedAt: created, ArchivedAt: archived})
	if !got.CreatedAt.Equal(created) || !got.ArchivedAt.Equal(archived) {
		t.Fatalf("times = %v, %v; want %v, %v", got.CreatedAt, got.ArchivedAt, created, archived)
	}
	if got := sessionInfo(store.Session{ID: "a2", CreatedAt: created}); !got.ArchivedAt.IsZero() {
		t.Fatalf("an unarchived row has ArchivedAt %v", got.ArchivedAt)
	}
}
