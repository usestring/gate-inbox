package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/workspec"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

type ciTestForge struct{ pr forge.PR }

func (f ciTestForge) PRs(refs []workspec.Ref) (map[string]forge.PR, forge.Health) {
	prs := map[string]forge.PR{}
	for _, ref := range refs {
		if ref.Kind == workspec.KindPR {
			prs[ref.Key()] = f.pr
		}
	}
	return prs, forge.Health{OK: true}
}

func TestCIFeedbackOptInUsesBoardRefreshAndDisablingRetiresQueue(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const url = "https://github.com/example/repo/pull/7"
	at := time.Now()
	if err := st.CreateSession(store.Session{ID: "owner", Tool: "claude", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordOpenedPRs("owner", []string{url}, at); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Tools: map[string]config.Tool{"claude": {}}}
	refresh := func() {
		t.Helper()
		tracker := newWorkTracker(cfg, st)
		tracker.GitHub = ciTestForge{pr: forge.PR{URL: url, State: forge.PROpen, Checks: forge.ChecksFailing, FetchedAt: at,
			CI: &forge.CISnapshot{HeadSHA: "current", Complete: true, Checks: []forge.CICheck{{
				ID: "run", Name: "tests", Status: "COMPLETED", Conclusion: "FAILURE",
			}}}}}
		session := worktracker.Session{ID: "owner", Text: url, Live: true}
		tracker.Discover(session)
		tracker.Refresh([]worktracker.Session{session})
	}
	queued := func() int {
		t.Helper()
		n, err := st.QueuedCount("owner")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	refresh()
	if n := queued(); n != 0 {
		t.Fatalf("default config sent CI feedback: %d messages", n)
	}
	cfg.Work.CIFeedback = true
	refresh()
	if n := queued(); n != 1 {
		t.Fatalf("enabled board did not queue feedback: %d messages", n)
	}
	cfg.Work.CIFeedback = false
	newWorkTracker(cfg, st)
	if n := queued(); n != 0 {
		t.Fatalf("disabling left feedback queued: %d messages", n)
	}
}

func TestPRFeedbackOptInUsesBoardRefreshAndDisablingRetiresQueue(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const url = "https://github.com/example/repo/pull/7"
	at := time.Unix(200, 0)
	if err := st.CreateSession(store.Session{ID: "owner", Tool: "agent", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordOpenedPRs("owner", []string{url}, at); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Tools: map[string]config.Tool{"agent": {}}}
	refresh := func() {
		t.Helper()
		tracker := newWorkTracker(cfg, st)
		tracker.GitHub = ciTestForge{pr: forge.PR{URL: url, State: forge.PROpen, FetchedAt: at,
			Feedback: &forge.PRFeedbackSnapshot{HeadSHA: "head", BaseSHA: "base", Mergeability: "CONFLICTING"}}}
		session := worktracker.Session{ID: "owner", Text: url, Live: true}
		tracker.Discover(session)
		tracker.Refresh([]worktracker.Session{session})
	}
	queued := func() int {
		t.Helper()
		n, err := st.QueuedCount("owner")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	refresh()
	if n := queued(); n != 0 {
		t.Fatalf("default feedback: %d", n)
	}
	cfg.Work.PRFeedback = true
	refresh()
	if n := queued(); n != 1 {
		t.Fatalf("enabled feedback: %d", n)
	}
	cfg.Work.PRFeedback = false
	newWorkTracker(cfg, st)
	if n := queued(); n != 0 {
		t.Fatalf("disabled feedback still queued: %d", n)
	}
}
