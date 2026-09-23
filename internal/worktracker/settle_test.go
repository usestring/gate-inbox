package worktracker

import (
	"errors"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
)

type fakeSeen struct {
	seen  map[string]time.Time
	fail  bool
	loads int
}

func (f *fakeSeen) WorkSeen() (map[string]time.Time, error) {
	f.loads++
	if f.fail {
		return nil, errors.New("closed")
	}
	out := map[string]time.Time{}
	for k, v := range f.seen {
		out[k] = v
	}
	return out, nil
}

func (f *fakeSeen) RecordWorkSeen(seen map[string]time.Time) error {
	if f.fail {
		return errors.New("closed")
	}
	if f.seen == nil {
		f.seen = map[string]time.Time{}
	}
	for k, v := range seen {
		if _, ok := f.seen[k]; !ok {
			f.seen[k] = v
		}
	}
	return nil
}

func (f *fakeSeen) ForgetWorkSeen(keys []string) error {
	if f.fail {
		return errors.New("closed")
	}
	for _, k := range keys {
		delete(f.seen, k)
	}
	return nil
}

const (
	mergedKey = "pr:example-org/sample-repo#838"
	doneKey   = "ticket:ABC-133756"
)

// settledBoard is one session on a merged pull request, a done ticket and an
// open pull request, resolved once.
func settledBoard(t *testing.T, now *time.Time) (*Tracker, *fakeForge, []Session) {
	t.Helper()
	f := &fakeForge{
		health: forge.Health{OK: true},
		prs: map[string]forge.PR{
			mergedKey:                        {Repo: "example-org/sample-repo", Number: 838, State: forge.PRMerged},
			"pr:example-org/sample-repo#839": {Repo: "example-org/sample-repo", Number: 839, State: forge.PROpen},
		},
		tickets: map[string]forge.Ticket{
			doneKey: {Identifier: "ABC-133756", StateType: "completed"},
		},
	}
	tr := tracker(t, fakeGit{branch: "abc-133756-fork", remote: "git@github.com:example-org/sample-repo.git"}, f, now)
	sessions := []Session{{ID: "s", Dir: "/repo", Text: "PR #838 and PR #839", Live: true}}
	tr.Discover(sessions[0])
	tr.Refresh(sessions)
	return tr, f, sessions
}

func retired(tr *Tracker) map[string]bool { return tr.For("s").Retired }

// Nothing leaves the board because it settled. It leaves because it settled,
// the operator had it on screen that way, and a day went by.
func TestASettledArtifactRetiresADayAfterItsFirstSighting(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tr, _, sessions := settledBoard(t, &now)

	if r := retired(tr); len(r) != 0 {
		t.Fatalf("retired before any sighting: %v", r)
	}
	tr.MarkSeen([]string{mergedKey, doneKey, "pr:example-org/sample-repo#839"})
	if r := retired(tr); len(r) != 0 {
		t.Fatalf("retired on sight: %v", r)
	}

	now = now.Add(DefaultSettleAfter - time.Minute)
	if r := retired(tr); len(r) != 0 {
		t.Fatalf("retired early: %v", r)
	}
	now = now.Add(2 * time.Minute)
	tr.Refresh(sessions)
	r := retired(tr)
	if !r[mergedKey] || !r[doneKey] {
		t.Fatalf("settled artifacts not retired: %v", r)
	}
	if r["pr:example-org/sample-repo#839"] {
		t.Fatal("an open pull request retired")
	}
}

// A sighting is of a settled artifact; a later one does not move the clock,
// and an open artifact takes no mark at all, so a merge after the sighting
// starts the clock at the sighting that sees it merged.
func TestOnlyTheFirstSettledSightingCounts(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tr, f, sessions := settledBoard(t, &now)
	open := "pr:example-org/sample-repo#839"

	tr.MarkSeen([]string{open})
	now = now.Add(time.Hour)
	f.prs[open] = forge.PR{Repo: "example-org/sample-repo", Number: 839, State: forge.PRMerged}
	tr.Refresh(sessions)
	tr.MarkSeen([]string{open})
	now = now.Add(time.Hour)
	tr.MarkSeen([]string{open})

	now = now.Add(DefaultSettleAfter - 2*time.Hour)
	if retired(tr)[open] {
		t.Fatal("the clock ran from a sighting of the open pull request")
	}
	now = now.Add(2 * time.Hour)
	if !retired(tr)[open] {
		t.Fatal("the clock did not run from the first settled sighting")
	}
}

// A reopened pull request is live work again. Its mark goes, and the next
// time it settles the clock starts over.
func TestReopeningClearsTheSighting(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tr, f, sessions := settledBoard(t, &now)
	tr.MarkSeen([]string{mergedKey})

	now = now.Add(2 * DefaultSettleAfter)
	f.prs[mergedKey] = forge.PR{Repo: "example-org/sample-repo", Number: 838, State: forge.PROpen}
	tr.Refresh(sessions)
	if retired(tr)[mergedKey] {
		t.Fatal("a reopened pull request stayed retired")
	}

	f.prs[mergedKey] = forge.PR{Repo: "example-org/sample-repo", Number: 838, State: forge.PRMerged}
	now = now.Add(2 * DefaultSettleAfter)
	tr.Refresh(sessions)
	if retired(tr)[mergedKey] {
		t.Fatal("the old sighting survived the reopen")
	}
}

// The sightings outlive the process, and the store forgets what the tracker
// forgets.
func TestSightingsAreStoredAndPrunedWithTheTracker(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &fakeSeen{}
	tr, f, sessions := settledBoard(t, &now)
	tr.WithSeen(store)
	tr.MarkSeen([]string{mergedKey, doneKey})
	if len(store.seen) != 0 {
		t.Fatal("a sighting was written on the event loop")
	}
	tr.Refresh(sessions)
	if _, ok := store.seen[mergedKey]; !ok {
		t.Fatalf("refresh did not write the sighting: %v", store.seen)
	}

	now = now.Add(2 * DefaultSettleAfter)
	again := tracker(t, fakeGit{branch: "abc-133756-fork", remote: "git@github.com:example-org/sample-repo.git"}, f, &now).WithSeen(store)
	again.Discover(sessions[0])
	again.Refresh(sessions)
	if r := retired(again); !r[mergedKey] || !r[doneKey] {
		t.Fatalf("a restart lost the sightings: %v", r)
	}

	again.Refresh(nil)
	if len(store.seen) != 0 {
		t.Fatalf("sightings for a forgotten session survived: %v", store.seen)
	}
}

// A store that cannot be written is retried, and a store that cannot be read
// leaves the tracker on memory.
func TestAFailingSeenStoreIsRetriedNotFatal(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &fakeSeen{fail: true}
	tr, _, sessions := settledBoard(t, &now)
	tr.WithSeen(store)
	tr.MarkSeen([]string{mergedKey})
	tr.Refresh(sessions)
	store.fail = false
	tr.Refresh(sessions)
	if _, ok := store.seen[mergedKey]; !ok {
		t.Fatalf("the failed write was not retried: %v", store.seen)
	}
}

// SettleAfter is the operator's; zero is the default rather than "at once".
func TestSettleAfterIsConfigurableAndZeroIsTheDefault(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tr, _, _ := settledBoard(t, &now)
	tr.SettleAfter = time.Hour
	tr.MarkSeen([]string{mergedKey})
	now = now.Add(time.Hour)
	if !retired(tr)[mergedKey] {
		t.Fatal("an hour's setting did not retire after an hour")
	}
	tr.SettleAfter = 0
	if retired(tr)[mergedKey] {
		t.Fatal("zero retired at once")
	}
}
