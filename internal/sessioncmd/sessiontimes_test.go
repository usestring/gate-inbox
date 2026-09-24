package sessioncmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// A Session carries the row's creation and archive times to Go callers, and
// the tools' JSON stays as it was.
func TestSessionCarriesTimesOutsideItsJSON(t *testing.T) {
	created := time.Unix(1_700_000_000, 0)
	archived := created.Add(time.Hour)
	got := (&runtime{}).sessionInfo(store.Session{ID: "a1", Archived: true, CreatedAt: created, ArchivedAt: archived}, false, false)
	if !got.CreatedAt.Equal(created) || !got.ArchivedAt.Equal(archived) {
		t.Fatalf("times = %v, %v; want %v, %v", got.CreatedAt, got.ArchivedAt, created, archived)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "2023") || strings.Contains(strings.ToLower(string(body)), "created") {
		t.Fatalf("the JSON carries the times: %s", body)
	}
}
