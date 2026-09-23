package worktracker

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/workspec"
)

// prSession is a session working on one pull request, named so the reference is predictable.
func prSession(id string, number int, live, visible bool) Session {
	return Session{
		ID:      id,
		Dir:     "/repo",
		Text:    fmt.Sprintf("PR #%d", number),
		Live:    live,
		Visible: visible,
	}
}

func discovered(tr *Tracker, sessions ...Session) []Session {
	for _, session := range sessions {
		tr.Discover(session)
	}
	return sessions
}

// What the operator is looking at is re-read every tick; what is merely running is not.
func TestAVisibleSessionRefreshesFasterThanARunningOne(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	watched := prSession("watched", 1, true, true)
	running := prSession("running", 2, true, false)
	sessions := discovered(tr, watched, running)
	tr.Refresh(sessions)

	// Past the visible interval, well short of the live one.
	now = now.Add(VisibleInterval + time.Second)
	tr.Refresh(sessions)

	asked := f.asked[len(f.asked)-1]
	if len(asked) != 1 {
		t.Fatalf("asked about %d references, want only the visible one: %+v", len(asked), asked)
	}
	if asked[0].Number != 1 {
		t.Errorf("asked about #%d, want the visible #1", asked[0].Number)
	}
}

// A reference two sessions name is as visible as its strongest claim. Anything else would let a
// background session's slow interval hold back a pull request that is on screen.
func TestAReferenceTakesTheStrongestClaimOnIt(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	// The idle session is listed first, so an implementation that takes the first claim it
	// sees rather than the strongest one fails here.
	sessions := discovered(tr,
		prSession("ended", 7, false, false),
		prSession("watched", 7, true, true))
	tr.Refresh(sessions)

	now = now.Add(VisibleInterval + time.Second)
	tr.Refresh(sessions)

	if len(f.asked[len(f.asked)-1]) != 1 {
		t.Error("a reference on screen was held to the interval of a session nobody is watching")
	}
}

// A merged pull request is not going to change, and a board carries them forever: they were the
// largest standing cost in the old two-tier scheme.
func TestAFinishedPullRequestIsNotReReadOnTheOrdinaryInterval(t *testing.T) {
	now := time.Unix(1700000000, 0)
	merged := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 1}
	f := &fakeForge{
		health: forge.Health{OK: true},
		prs:    map[string]forge.PR{merged.Key(): {Repo: "example-org/sample-repo", Number: 1, State: forge.PRMerged}},
	}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	// Visible, which is the strongest claim there is: finished has to outrank it.
	sessions := discovered(tr, prSession("watched", 1, true, true))
	tr.Refresh(sessions)
	if f.calls != 1 {
		t.Fatalf("calls = %d, want the first look", f.calls)
	}

	now = now.Add(IdleInterval * 2)
	tr.Refresh(sessions)
	if f.calls != 1 {
		t.Errorf("a merged pull request was re-read after %v", IdleInterval*2)
	}

	now = now.Add(TerminalInterval)
	tr.Refresh(sessions)
	if f.calls != 2 {
		t.Errorf("calls = %d, want one more read once the terminal interval passed", f.calls)
	}
}

// One pass cannot spend the whole quota. A board that has been idle for an hour comes back with
// everything due at once, and an unbounded batch is an unbounded number of GraphQL points.
func TestOnePassIsCapped(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	var sessions []Session
	for i := 0; i < RefreshBudget*2; i++ {
		sessions = append(sessions, prSession(fmt.Sprintf("s%d", i), i+1, true, false))
	}
	sessions = discovered(tr, sessions...)
	tr.Refresh(sessions)

	if got := len(f.asked[0]); got != RefreshBudget {
		t.Errorf("asked about %d references in one pass, want the %d budget", got, RefreshBudget)
	}
}

// And the cap takes the visible ones first, which is the whole point of ordering: on a board of two
// hundred references the one under the cursor must not wait for the fourth pass.
func TestTheCapTakesWhatIsVisibleFirst(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	// The visible session is last in the slice, so only the ordering can save it.
	var sessions []Session
	for i := 0; i < RefreshBudget*2; i++ {
		sessions = append(sessions, prSession(fmt.Sprintf("s%d", i), i+1, true, false))
	}
	watched := prSession("watched", 9999, true, true)
	sessions = discovered(tr, append(sessions, watched)...)
	tr.Refresh(sessions)

	var found bool
	for _, ref := range f.asked[0] {
		if ref.Number == 9999 {
			found = true
		}
	}
	if !found {
		t.Error("the visible reference was cut from a capped pass")
	}
}

// The budget is per source: a board thick with pull requests must not starve its tickets, because
// GitHub and Linear have separate quotas and one cannot spend the other's.
func TestTheBudgetIsPerSource(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	var sessions []Session
	for i := 0; i < RefreshBudget*2; i++ {
		sessions = append(sessions, prSession(fmt.Sprintf("s%d", i), i+1, true, false))
	}
	tickets := Session{ID: "tickets", Dir: "/repo", Live: true,
		Text: strings.Join([]string{"ABC-1", "ABC-2", "ABC-3"}, " ")}
	sessions = discovered(tr, append(sessions, tickets)...)
	tr.Refresh(sessions)

	var asked int
	for _, ref := range f.asked[0] {
		if ref.Kind == workspec.KindTicket {
			asked++
		}
	}
	if asked != 3 {
		t.Errorf("asked about %d tickets, want all 3 — the pull requests spent their own budget", asked)
	}
}
