package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func TestOpenedPRsAreRememberedOnceAndDroppedWithTheSession(t *testing.T) {
	st, err := Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateSession(Session{ID: "s1", Name: "one", Tool: "claude", Cwd: "/repo", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	first := []string{"https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2"}
	if err := st.RecordOpenedPRs("s1", first, now); err != nil {
		t.Fatal(err)
	}
	// The same set again, plus one more, on a later tick: no duplicates,
	// and the earlier rows keep their order.
	if err := st.RecordOpenedPRs("s1", append(first, "https://github.com/o/r/pull/3", ""), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := st.OpenedPRs("s1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://github.com/o/r/pull/1", "https://github.com/o/r/pull/2", "https://github.com/o/r/pull/3"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if none, _ := st.OpenedPRs("other"); len(none) != 0 {
		t.Errorf("another session sees %v", none)
	}
	if err := st.Delete("s1"); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.OpenedPRs("s1"); len(left) != 0 {
		t.Errorf("rows outlived the session: %v", left)
	}
}
