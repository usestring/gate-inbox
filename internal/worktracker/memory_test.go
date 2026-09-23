package worktracker

import (
	"errors"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/workspec"
)

// fakeMemory is what the last run left behind, without a database.
type fakeMemory struct {
	prs     []store.StoredPR
	tickets []store.StoredTicket
	loadErr error

	savedPRs     []store.StoredPR
	savedTickets []store.StoredTicket
	saves        int
}

func (m *fakeMemory) ForgeState(time.Time) ([]store.StoredPR, []store.StoredTicket, error) {
	return m.prs, m.tickets, m.loadErr
}

func (m *fakeMemory) SaveForgeState(prs []store.StoredPR, tickets []store.StoredTicket) error {
	m.saves++
	m.savedPRs = append(m.savedPRs, prs...)
	m.savedTickets = append(m.savedTickets, tickets...)
	return nil
}

func restoredTracker(t *testing.T, memory *fakeMemory, f *fakeForge, now *time.Time) *Tracker {
	t.Helper()
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, now)
	tr.Memory = memory
	if err := tr.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	return tr
}

// The point of storing anything: the work column has content before the first network call, so a
// restart is not a blank board plus the largest burst of quota the inbox ever spends.
func TestRestoredStateIsOnTheBoardBeforeAnythingIsAsked(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ref := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 7394}
	memory := &fakeMemory{prs: []store.StoredPR{{
		Key: ref.Key(), Repo: "example-org/sample-repo", Number: 7394, Title: "Fix it",
		State: "open", Checks: "failing", FetchedAt: now.Add(-10 * time.Minute),
	}}}
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := restoredTracker(t, memory, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: true}
	tr.Discover(session)

	work := tr.For("s")
	pr, ok := work.PRs[ref.Key()]
	if !ok {
		t.Fatal("nothing on the board before the first refresh")
	}
	if pr.Title != "Fix it" || pr.Checks != "failing" {
		t.Errorf("restored %+v, want the remembered answer", pr)
	}
	if f.calls != 0 {
		t.Errorf("asked GitHub %d times during a restore", f.calls)
	}
}

// The whole safety argument. A restored value is drawn, but it is not *settled*: Looked stays false,
// which is what the view reads to stamp it "as of 09:12" instead of stating it as current.
func TestARestoredReferenceIsNotTreatedAsLookedAt(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ref := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 7394}
	memory := &fakeMemory{prs: []store.StoredPR{{
		Key: ref.Key(), Repo: "example-org/sample-repo", Number: 7394, State: "merged",
		FetchedAt: now.Add(-10 * time.Minute),
	}}}
	tr := restoredTracker(t, memory, &fakeForge{health: forge.Health{OK: true}}, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: true}
	tr.Discover(session)

	if tr.For("s").Looked[ref.Key()] {
		t.Error("a value nobody has verified this run reports as looked at")
	}
}

// And because it is not settled, it is due at once: the board corrects itself on the first tick
// rather than showing a remembered value for a whole interval.
func TestARestoredReferenceIsDueImmediately(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ref := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 7394}
	memory := &fakeMemory{prs: []store.StoredPR{{
		Key: ref.Key(), Repo: "example-org/sample-repo", Number: 7394, State: "open",
		FetchedAt: now.Add(-time.Second),
	}}}
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := restoredTracker(t, memory, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: true}
	tr.Discover(session)
	tr.Refresh([]Session{session})

	if f.calls != 1 {
		t.Fatalf("calls = %d, want the restored reference verified on the first tick", f.calls)
	}
	if !tr.For("s").Looked[ref.Key()] {
		t.Error("still unverified after a successful refresh")
	}
}

// Only what this run confirmed is written back. Re-saving a restored row would let a value live
// forever by being repeatedly stamped with a fresh time it was never checked at.
func TestOnlyFreshlyResolvedStateIsWrittenBack(t *testing.T) {
	now := time.Unix(1700000000, 0)
	stale := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 1}
	memory := &fakeMemory{prs: []store.StoredPR{{
		Key: stale.Key(), Repo: "example-org/sample-repo", Number: 1, State: "open",
		FetchedAt: now.Add(-time.Hour),
	}}}

	fresh := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 7394}
	f := &fakeForge{
		health: forge.Health{OK: true},
		prs: map[string]forge.PR{fresh.Key(): {
			Repo: "example-org/sample-repo", Number: 7394, State: forge.PROpen, FetchedAt: now,
		}},
	}
	tr := restoredTracker(t, memory, f, &now)

	// Both are on the board, so the restored one is live in the cache and reachable. Only
	// 7394 comes back from the source, so only 7394 was confirmed this run.
	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394 and PR #1", Live: true}
	tr.Discover(session)
	if _, ok := tr.For("s").PRs[stale.Key()]; !ok {
		t.Fatal("the restored reference is not in the cache, so this proves nothing")
	}
	tr.Refresh([]Session{session})

	if len(memory.savedPRs) != 1 {
		t.Fatalf("wrote %d rows, want only what this run resolved", len(memory.savedPRs))
	}
	if memory.savedPRs[0].Key != fresh.Key() {
		t.Errorf("wrote %q, want the freshly resolved %q", memory.savedPRs[0].Key, fresh.Key())
	}
}

// A tracker with nothing to remember with is the ordinary in-process case, not a special one.
func TestATrackerWithNoMemoryStillWorks(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tr := tracker(t, fakeGit{}, &fakeForge{health: forge.Health{OK: true}}, &now)
	if err := tr.Restore(); err != nil {
		t.Errorf("restore without memory: %v", err)
	}
	session := Session{ID: "s", Text: "PR #7394", Live: true}
	tr.Discover(session)
	tr.Refresh([]Session{session})
}

// A database that cannot be read starts the board cold, which is what it did before any of this
// existed. It is not worth failing a launch over.
func TestAnUnreadableMemoryIsReportedRatherThanFatal(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tr := tracker(t, fakeGit{}, &fakeForge{health: forge.Health{OK: true}}, &now)
	tr.Memory = &fakeMemory{loadErr: errors.New("database is locked")}

	if err := tr.Restore(); err == nil {
		t.Error("a failed restore reported success")
	}
	session := Session{ID: "s", Text: "PR #7394", Live: true}
	tr.Discover(session)
	tr.Refresh([]Session{session})
}
