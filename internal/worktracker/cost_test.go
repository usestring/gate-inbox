package worktracker

import (
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"github.com/usestring/gate-inbox/internal/tracetest"
	"github.com/usestring/gate-inbox/internal/workspec"
)

// What the board actually does: many sessions, few directories, every tick.
//
// Measured on the live board, forty-seven sessions sat on twenty-two distinct
// working directories and discovery forked four git processes for each session
// on every pass -- thirty-two thousand processes an hour, of which the
// directories themselves justified a few hundred. Nothing about a directory's
// remote or its .gitmodules changes between two sessions that share it, or
// between two ticks a second apart, so the second session onwards must cost
// nothing.
func TestDirectoriesAreForkedForOnceAcrossSessionsAndTicks(t *testing.T) {
	requireGit(t)
	dir := repo(t, filepath.Join(tmuxtest.ScratchDir(t), "sample-repo"), "git@github.com:example-org/sample-repo.git")

	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, NewGit(), f, &now)

	const sessions, ticks = 6, 4
	spans := tracetest.Capture(t)
	for tick := range ticks {
		for session := range sessions {
			tr.Discover(Session{ID: string(rune('a'+session)) + string(rune('0'+tick)), Dir: dir})
		}
	}
	forks := tracetest.Named(spans(), "worktracker.git")

	// Four for the directory, plus whatever Names spends the first time it
	// reads the short names. Both are cached, so the bound is a constant and
	// emphatically not a multiple of the twenty-four visits.
	const bound = 8
	if len(forks) > bound {
		t.Errorf("%d sessions over %d ticks on one directory forked git %d times, want at most %d: "+
			"the directory is read once, not once per visit", sessions, ticks, len(forks), bound)
	}
	if len(forks) == 0 {
		t.Error("no git forks at all, so this test is not measuring anything")
	}
}

// The saving may not cost the board a checkout.
//
// A cached branch is the one local fact that goes wrong while somebody is
// working: they check a ticket branch out precisely to start working it, and a
// board still showing the branch before it is showing the wrong ticket with a
// real title beside it. So the cache is not held by a clock here -- git
// rewrites HEAD on checkout, and an entry whose HEAD has moved is not an entry
// any more. Nothing here advances the tracker's clock, so a cache trusting its
// interval alone passes the first half and fails the second.
func TestACheckoutIsSeenOnTheNextPassAndNotAnIntervalLater(t *testing.T) {
	requireGit(t)
	dir := repo(t, filepath.Join(tmuxtest.ScratchDir(t), "sample-repo"), "git@github.com:example-org/sample-repo.git")
	git(t, dir, "checkout", "-q", "-b", "alice/abc-111111-first")

	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, NewGit(), f, &now)

	if got := branchTicket(t, tr.Discover(Session{ID: "s", Dir: dir})); got != "ABC-111111" {
		t.Fatalf("branch ticket = %q, want ABC-111111 -- the fixture is wrong", got)
	}

	git(t, dir, "checkout", "-q", "-b", "alice/abc-222222-second")

	if got := branchTicket(t, tr.Discover(Session{ID: "s", Dir: dir})); got != "ABC-222222" {
		t.Errorf("after checking out the second branch the board still says %q, want ABC-222222: "+
			"a cached branch outliving its checkout puts a real, wrong ticket on the row", got)
	}
}

// branchTicket is the ticket discovery read off the checked-out branch.
func branchTicket(t *testing.T, refs []workspec.Ref) string {
	t.Helper()
	for _, ref := range refs {
		if ref.Kind == workspec.KindTicket && ref.Provenance == workspec.FromBranch {
			return ref.Identifier
		}
	}
	return ""
}

// A pass waits for the slower source, not for both of them in turn.
//
// GitHub and Linear share nothing: two services, two connections, two quotas,
// and neither one's answer is an input to the other. Asked in turn they cost
// the board the sum of two waits -- on the live board a median pass of 1489ms
// over a median GitHub of 908ms and a median Linear of 445ms, and a worst pass
// of 11168ms that was a ten-second Linear timeout with a GitHub queued behind
// it.
//
// Each source here blocks until it has seen the other arrive, so a pass that
// asks them in turn cannot finish at all and the test fails on its deadline.
func TestTheTwoSourcesAreAskedAtTheSameTime(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &meetingForge{ghAsked: make(chan struct{}), linAsked: make(chan struct{})}
	tr := New(fakeGit{branch: "alice/abc-133756-slug", remote: "git@github.com:example-org/sample-repo.git"}, f, f)
	tr.Now = func() time.Time { return now }
	// A ticket off the branch and a pull request the session opened, so both
	// sources have something to be asked about.
	tr.Discover(Session{ID: "s", Dir: "/repo", Text: "https://github.com/example-org/sample-repo/pull/7585\n"})

	done := make(chan struct{})
	go func() {
		tr.Refresh([]Session{{ID: "s", Dir: "/repo", Live: true}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the refresh never returned: the two sources are still being asked in turn")
	}

	gh, linear := f.met()
	if !gh || !linear {
		t.Errorf("GitHub saw Linear being asked: %v; Linear saw GitHub: %v -- want both. "+
			"A source that waited out the rendezvous was the only one in flight, "+
			"which is a pass paying the sum of two waits", gh, linear)
	}
}

// meetingForge is both sources. Each announces that it is being asked and then
// waits to see the other announce, which can only happen if the pass did not
// wait for the first to return before asking the second.
type meetingForge struct {
	ghAsked, linAsked    chan struct{}
	mu                   sync.Mutex
	metGitHub, metLinear bool
}

// rendezvousWait is long enough that a slow machine does not fail this, and
// short enough that a serial pass fails on the assertion rather than on the
// test's own deadline -- which would read as a hang rather than as the defect.
const rendezvousWait = 3 * time.Second

func (m *meetingForge) PRs([]workspec.Ref) (map[string]forge.PR, forge.Health) {
	close(m.ghAsked)
	met := waitFor(m.linAsked)
	m.mu.Lock()
	m.metGitHub = met
	m.mu.Unlock()
	return map[string]forge.PR{}, forge.Health{OK: true}
}

func (m *meetingForge) Tickets([]workspec.Ref) (map[string]forge.Ticket, forge.Health) {
	close(m.linAsked)
	met := waitFor(m.ghAsked)
	m.mu.Lock()
	m.metLinear = met
	m.mu.Unlock()
	return map[string]forge.Ticket{}, forge.Health{OK: true}
}

func (m *meetingForge) met() (github, linear bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.metGitHub, m.metLinear
}

func waitFor(announced chan struct{}) bool {
	select {
	case <-announced:
		return true
	case <-time.After(rendezvousWait):
		return false
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}
