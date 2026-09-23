package worktracker

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/workspec"
)

// churnForge answers for whatever it is asked about, so a soak that invents a
// fresh reference per generation gets a fresh resolved artifact per generation
// -- which is what a real board does as sessions come and go.
type churnForge struct{}

func (churnForge) PRs(refs []workspec.Ref) (map[string]forge.PR, forge.Health) {
	out := map[string]forge.PR{}
	for _, ref := range refs {
		if ref.Kind != workspec.KindPR {
			continue
		}
		out[ref.Key()] = forge.PR{
			Repo:   ref.Repo,
			Number: ref.Number,
			Title:  fmt.Sprintf("a pull request title of roughly realistic length for %s", ref.Key()),
			URL:    "https://github.com/" + ref.Repo + "/pull/" + fmt.Sprint(ref.Number),
		}
	}
	return out, forge.Health{OK: true}
}

func (churnForge) Tickets(refs []workspec.Ref) (map[string]forge.Ticket, forge.Health) {
	out := map[string]forge.Ticket{}
	for _, ref := range refs {
		if ref.Kind != workspec.KindTicket {
			continue
		}
		out[ref.Key()] = forge.Ticket{
			Identifier: ref.Identifier,
			Title:      fmt.Sprintf("a ticket title of roughly realistic length for %s", ref.Identifier),
			State:      "In Progress",
			StateType:  "started",
			URL:        "https://linear.app/x/issue/" + ref.Identifier,
		}
	}
	return out, forge.Health{OK: true}
}

// churn drives generations of sessions through the tracker the way
// Model.refreshWork does: Discover for each session that is live right now,
// then one Refresh for the whole set. Each generation's ids and references are
// distinct, so every previous generation is a board of sessions that ended.
// firstGen is where the generation counter starts, so two successive stretches
// of churn invent disjoint sessions. Sharing the counter was a real defect in
// an earlier draft of this test: the second stretch replayed the first
// stretch's ids, so it overwrote the leaked entries instead of adding to them
// and the heap assertion passed on a tracker that was leaking outright.
func churn(t testing.TB, tr *Tracker, now *time.Time, firstGen, generations, perGeneration int) {
	t.Helper()
	for gen := firstGen; gen < firstGen+generations; gen++ {
		sessions := make([]Session, 0, perGeneration)
		for i := 0; i < perGeneration; i++ {
			sess := Session{
				ID: fmt.Sprintf("sess-%d-%d", gen, i),
				// One PR and one ticket per session, each unique to the
				// generation, which is what a board of real work looks like.
				Text: fmt.Sprintf(
					"working on https://github.com/example-org/sample-repo/pull/%d for ABC-%d",
					gen*1000+i, 100000+gen*1000+i),
				Live: true,
			}
			tr.Discover(sess)
			sessions = append(sessions, sess)
		}
		tr.Refresh(sessions)
		// Time moves so the next generation's references are due rather than
		// suppressed by the refresh interval.
		*now = now.Add(2 * IdleInterval)
	}
}

func (t *Tracker) sizes() (refs, prs, tickets, fetched int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.refs), len(t.prs), len(t.tickets), len(t.fetched)
}

// TestTrackerForgetsSessionsThatEnded is the leak, written down.
//
// The tracker's four maps had no delete in them anywhere. Discover wrote
// refs[session.ID] on every poll pass and nothing ever removed it, and Refresh
// wrote prs, tickets and fetched keyed by reference. A session that ends
// vanishes from the board and from m.sessions, so nothing ever visits its
// entries again -- but they stay resident for as long as the manager runs,
// which on the operator's board is days.
//
// The sensitivity is the point of the numbers below: 200 generations of 8
// sessions is 1,600 sessions and 3,200 references. Without pruning the maps
// hold all of them; with it they hold one generation. An assertion that merely
// said "not enormous" would have passed on the leak, so the bound is a small
// multiple of what is actually live.
func TestTrackerForgetsSessionsThatEnded(t *testing.T) {
	const (
		generations   = 200
		perGeneration = 8
	)
	now := time.Unix(1700000000, 0)
	tr := New(fakeGit{}, churnForge{}, churnForge{})
	tr.Now = func() time.Time { return now }

	churn(t, tr, &now, 0, generations, perGeneration)

	refs, prs, tickets, fetched := tr.sizes()

	// One entry per live session, and one PR + one ticket per live session.
	// Doubling each allows for a generation still settling without allowing
	// anything proportional to the number churned.
	wantRefs := perGeneration * 2
	wantArtifacts := perGeneration * 2
	if refs > wantRefs {
		t.Errorf("refs holds %d entries after %d sessions ended; want <= %d (one per live session)",
			refs, generations*perGeneration, wantRefs)
	}
	if prs > wantArtifacts {
		t.Errorf("prs holds %d entries; want <= %d", prs, wantArtifacts)
	}
	if tickets > wantArtifacts {
		t.Errorf("tickets holds %d entries; want <= %d", tickets, wantArtifacts)
	}
	// fetched carries both kinds, so its live population is twice the others.
	if fetched > wantArtifacts*2 {
		t.Errorf("fetched holds %d entries; want <= %d", fetched, wantArtifacts*2)
	}
}

// TestTrackerHeapDoesNotGrowWithChurn measures what the map counts imply, in
// bytes, so the verdict rests on a heap reading rather than only on a
// cardinality the implementation could satisfy while still retaining payloads.
//
// It compares two equal stretches of churn rather than a start and an end: the
// first settles the allocator and any lazily built state, and only the second
// is measured. A tracker that forgets ended sessions ends the second stretch
// holding what it held at the start of it.
func TestTrackerHeapDoesNotGrowWithChurn(t *testing.T) {
	const (
		generations   = 300
		perGeneration = 8
	)
	now := time.Unix(1700000000, 0)
	tr := New(fakeGit{}, churnForge{}, churnForge{})
	tr.Now = func() time.Time { return now }

	churn(t, tr, &now, 0, generations, perGeneration)
	before := liveHeap()

	churn(t, tr, &now, generations, generations, perGeneration)
	after := liveHeap()
	// Without this the measurement is of nothing. tr is not read again after
	// the churn above, so the collector inside liveHeap is entitled to free
	// the whole tracker before reading the heap -- and it does: the test
	// passed on a tracker leaking three megabytes until this line existed.
	runtime.KeepAlive(tr)

	// The retained payload of one leaked generation is on the order of a
	// kilobyte, so 300 leaked generations is hundreds of kilobytes: a 512 KB
	// allowance passes on a steady heap and fails on the leak with room to
	// spare. Growth is signed on purpose -- a heap that shrank is not a
	// failure.
	const allowance = 512 << 10
	if growth := int64(after) - int64(before); growth > allowance {
		t.Errorf("live heap grew %d bytes across %d further sessions (%d -> %d); want <= %d",
			growth, generations*perGeneration, before, after, allowance)
	}
}

// liveHeap is bytes still reachable after a collection. Two collections
// because the first can leave finalizable objects uncollected, and the reading
// has to be of what is genuinely retained rather than of what is merely not
// yet swept.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// TestTrackerForgetsOnAQuietBoard covers the ordering inside Refresh, which is
// the case a churn soak alone misses.
//
// Refresh returns early when nothing has aged out. If forgetting happened
// after that return, a board where every reference is freshly resolved would
// never forget anything -- and a quiet board is precisely when ended sessions
// accumulate fastest, because nothing is prompting a look. A mutant that moved
// the forget below the early return survived every other test in this file.
//
// So: resolve one generation, then let sessions end without advancing the
// clock. Nothing is due on any of the later passes, and the tracker must still
// have let go of what ended.
func TestTrackerForgetsOnAQuietBoard(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tr := New(fakeGit{}, churnForge{}, churnForge{})
	tr.Now = func() time.Time { return now }

	// One generation, resolved.
	churn(t, tr, &now, 0, 1, 8)

	// Further generations with the clock held still, so every reference they
	// carry is inside the refresh interval and nothing comes out due.
	frozen := now
	for gen := 1; gen < 50; gen++ {
		sessions := make([]Session, 0, 8)
		for i := 0; i < 8; i++ {
			sess := Session{
				ID:   fmt.Sprintf("quiet-%d-%d", gen, i),
				Text: "no references at all",
				Live: true,
			}
			tr.Discover(sess)
			sessions = append(sessions, sess)
		}
		tr.Refresh(sessions)
		now = frozen
	}

	refs, prs, tickets, fetched := tr.sizes()
	if refs > 8*2 {
		t.Errorf("refs holds %d entries on a quiet board; want <= %d", refs, 8*2)
	}
	// The first generation's artifacts belong to sessions that are gone, and
	// no live session refers to them.
	if prs != 0 || tickets != 0 || fetched != 0 {
		t.Errorf("artifacts of ended sessions survived a quiet board: prs=%d tickets=%d fetched=%d, want 0",
			prs, tickets, fetched)
	}
}
