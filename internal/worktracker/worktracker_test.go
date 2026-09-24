package worktracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"github.com/usestring/gate-inbox/internal/workspec"
)

type fakeGit struct {
	branch, remote string
	submodules     bool
	names          map[string]string
}

func (f fakeGit) Branch(string) (string, bool) { return f.branch, f.branch != "" }
func (f fakeGit) Remote(string) (string, bool) { return f.remote, f.remote != "" }
func (f fakeGit) HasSubmodules(string) bool    { return f.submodules }
func (f fakeGit) Names(string) map[string]string {
	return f.names
}

type fakeForge struct {
	prs     map[string]forge.PR
	tickets map[string]forge.Ticket
	health  forge.Health
	asked   [][]workspec.Ref
	calls   int
	// ticketCalls counts the ticket half separately: a source is asked only when something of
	// its kind is due, so a board carrying tickets alone is not a look at GitHub.
	ticketCalls int
	// ticketHealth lets one double stand in for two sources that disagree — a rate-limited
	// GitHub beside a Linear that is answering fine. Nil means both answer alike.
	ticketHealth *forge.Health
}

func (f *fakeForge) PRs(refs []workspec.Ref) (map[string]forge.PR, forge.Health) {
	f.calls++
	f.asked = append(f.asked, refs)
	return f.prs, f.health
}

func (f *fakeForge) Tickets(refs []workspec.Ref) (map[string]forge.Ticket, forge.Health) {
	f.ticketCalls++
	if f.ticketHealth != nil {
		return f.tickets, *f.ticketHealth
	}
	return f.tickets, f.health
}

func tracker(t *testing.T, git Git, f *fakeForge, now *time.Time) *Tracker {
	t.Helper()
	tr := New(git, f, f)
	tr.Now = func() time.Time { return *now }
	return tr
}

/* ---------------------------------------------------------------------------- discovery */

// The strongest evidence there is that a session is working a ticket: somebody checked that branch
// out to do the work, which no amount of talking about a ticket implies.
func TestTheBranchIsTheStrongestEvidence(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{branch: "alice/abc-133756-slug", remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	refs := tr.Discover(Session{ID: "s", Dir: "/repo", Text: "also mentions ABC-999999 in passing"})

	var ticket workspec.Ref
	for _, ref := range refs {
		if ref.Kind == workspec.KindTicket && ref.Provenance == workspec.FromBranch {
			ticket = ref
		}
	}
	if ticket.Identifier != "ABC-133756" {
		t.Fatalf("branch ticket = %+v, want ABC-133756 from the branch", ticket)
	}

	if lead := workspec.ByEvidence(refs)[0]; lead.Identifier != "ABC-133756" {
		t.Errorf("strongest reference = %+v, want the branch's ABC-133756", lead)
	}
}

// A pull request the session watched itself open outranks one it merely mentioned.
func TestOpeningAPullRequestOutranksMentioningOne(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	text := "I looked at PR #1 first.\nhttps://github.com/example-org/sample-repo/pull/7394\n"
	refs := tr.Discover(Session{ID: "s", Dir: "/repo", Text: text})

	if lead := workspec.ByEvidence(refs)[0]; lead.Number != 7394 || lead.Provenance != workspec.FromCreation {
		t.Errorf("strongest reference = %+v, want 7394 by creation", lead)
	}
}

// A URL inside a sentence is somebody talking about a pull request. A line that is only the URL is
// gh reporting what it just made.
func TestAUrlInProseIsAMentionNotACreation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{}, f, &now)

	refs := tr.Discover(Session{ID: "s", Text: "see https://github.com/example-org/sample-repo/pull/7394 for context"})
	if len(refs) != 1 {
		t.Fatalf("refs = %+v", refs)
	}
	if refs[0].Provenance != workspec.FromText {
		t.Errorf("provenance = %q, want text", refs[0].Provenance)
	}
}

// Without a repository to resolve it against, a bare "PR #7394" is dropped. A confidently wrong
// pull request on the board is worse than an empty column.
func TestABarePullReferenceNeedsARepository(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}

	withoutRemote := tracker(t, fakeGit{}, f, &now)
	if refs := withoutRemote.Discover(Session{ID: "s", Text: "fixed in PR #7394"}); len(refs) != 0 {
		t.Errorf("resolved %+v with no repository", refs)
	}

	withRemote := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)
	refs := withRemote.Discover(Session{ID: "s", Dir: "/repo", Text: "fixed in PR #7394"})
	if len(refs) != 1 || refs[0].Repo != "example-org/sample-repo" {
		t.Errorf("refs = %+v", refs)
	}
}

/* ----------------------------------------------------------------------------- refresh */

func TestRefreshResolvesWhatWasDiscovered(t *testing.T) {
	now := time.Unix(1700000000, 0)
	key := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 7394}.Key()
	f := &fakeForge{
		health: forge.Health{OK: true},
		prs:    map[string]forge.PR{key: {Repo: "example-org/sample-repo", Number: 7394, State: forge.PROpen}},
	}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: true}
	tr.Discover(session)
	tr.Refresh([]Session{session})

	work := tr.For("s")
	if len(work.PRs) != 1 {
		t.Fatalf("resolved %+v", work.PRs)
	}
	if !work.Looked[key] {
		t.Errorf("a resolved reference is not marked as looked up: %+v", work.Looked)
	}
}

// The sweep runs every 1.2 seconds. Nothing here may ride on it, so a second refresh inside the
// interval asks nobody anything.
func TestAFreshAnswerIsNotAskedForAgain(t *testing.T) {
	now := time.Unix(1700000000, 0)
	key := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 7394}.Key()
	f := &fakeForge{health: forge.Health{OK: true}, prs: map[string]forge.PR{key: {Number: 7394}}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: true}
	tr.Discover(session)

	tr.Refresh([]Session{session})
	now = now.Add(LiveInterval / 2)
	tr.Refresh([]Session{session})
	if f.calls != 1 {
		t.Fatalf("asked %d times inside one interval, want 1", f.calls)
	}

	now = now.Add(LiveInterval)
	tr.Refresh([]Session{session})
	if f.calls != 2 {
		t.Errorf("asked %d times after the interval, want 2", f.calls)
	}
}

// A pull request on a session that ended yesterday changes slowly and nobody is watching it.
func TestAnEndedSessionRefreshesFarLessOften(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: false}
	tr.Discover(session)
	tr.Refresh([]Session{session})

	now = now.Add(LiveInterval * 2)
	tr.Refresh([]Session{session})
	if f.calls != 1 {
		t.Errorf("an ended session refreshed at the live interval (%d calls)", f.calls)
	}

	now = now.Add(IdleInterval)
	tr.Refresh([]Session{session})
	if f.calls != 2 {
		t.Errorf("calls = %d, want a refresh once the idle interval passed", f.calls)
	}
}

// A board that blanks its work column because GitHub had a bad minute is worse than one showing a
// minute-old truth.
func TestAFailedLookKeepsTheLastGoodAnswer(t *testing.T) {
	now := time.Unix(1700000000, 0)
	key := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 7394}.Key()
	f := &fakeForge{
		health: forge.Health{OK: true},
		prs:    map[string]forge.PR{key: {Number: 7394, State: forge.PROpen}},
	}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: true}
	tr.Discover(session)
	tr.Refresh([]Session{session})

	f.prs, f.health = nil, forge.Health{Reason: "network is unreachable"}
	now = now.Add(LiveInterval * 2)
	tr.Refresh([]Session{session})

	work := tr.For("s")
	if len(work.PRs) != 1 || work.PRs[key].Number != 7394 {
		t.Errorf("lost the previous answer: %+v", work.PRs)
	}
	if github, _ := tr.Health(); github.OK {
		t.Error("health did not record that the look failed")
	}
}

// Two sessions on the same pull request is one question, not two.
func TestTheSameReferenceIsAskedAboutOnce(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	for _, id := range []string{"a", "b"} {
		tr.Discover(Session{ID: id, Dir: "/repo", Text: "PR #7394", Live: true})
	}
	tr.Refresh([]Session{{ID: "a", Live: true}, {ID: "b", Live: true}})

	if len(f.asked) != 1 || len(f.asked[0]) != 1 {
		t.Errorf("asked %+v, want one reference once", f.asked)
	}
}

func TestASessionWithNothingToShowHasNothing(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{branch: "main", remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	tr.Discover(Session{ID: "s", Dir: "/repo", Text: "just working"})
	work := tr.For("s")

	if len(work.Refs) != 0 || work.NeedsYou() {
		t.Errorf("work = %+v", work)
	}
	if len(work.PRs) != 0 || len(work.Tickets) != 0 || len(work.Looked) != 0 {
		t.Errorf("work invented %+v / %+v / %+v", work.PRs, work.Tickets, work.Looked)
	}
}

func TestNeedsYouSurfacesAFailingCheck(t *testing.T) {
	now := time.Unix(1700000000, 0)
	key := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 7394}.Key()
	f := &fakeForge{
		health: forge.Health{OK: true},
		prs: map[string]forge.PR{key: {
			Number: 7394, State: forge.PROpen, Checks: forge.ChecksFailing, Mergeable: true,
		}},
	}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: true}
	tr.Discover(session)
	tr.Refresh([]Session{session})

	if !tr.For("s").NeedsYou() {
		t.Error("a red check did not read as needing you")
	}
}

// A reference that resolves to nothing must still count as looked-at. workspec's ticket pattern
// matches anything shaped like ABC-123 and plenty of those name no ticket, so stamping only what
// came back would leave them permanently due — every tick asking again, forever, about references
// that will never resolve.
func TestAReferenceThatResolvesToNothingIsStillNotAskedAgain(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}} // healthy, and finds nothing
	tr := tracker(t, fakeGit{}, f, &now)

	session := Session{ID: "s", Text: "closes ABC-123", Live: true}
	tr.Discover(session)

	tr.Refresh([]Session{session})
	now = now.Add(LiveInterval / 2)
	tr.Refresh([]Session{session})

	if f.ticketCalls != 1 {
		t.Errorf("asked %d times about a reference that resolves to nothing, want 1", f.ticketCalls)
	}
}

// A failed look is not a look. It must retry rather than wait out the interval on an answer it
// never got.
func TestAFailedLookRetriesRatherThanWaitingOutTheInterval(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{Reason: "network is unreachable"}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: true}
	tr.Discover(session)

	tr.Refresh([]Session{session})
	now = now.Add(time.Second)
	tr.Refresh([]Session{session})

	if f.calls != 2 {
		t.Errorf("calls = %d, want the failed look retried", f.calls)
	}
}

/* ------------------------------------------------------- submodules and bare pull requests */

// git runs a git command in dir, failing the test rather than the tracker.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(tmuxtest.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_ALLOW_PROTOCOL=file",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// repo makes a one-commit git repository with the given origin.
func repo(t *testing.T, dir, origin string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "init")
	return dir
}

// superproject builds the real thing this bug lives in: a repository whose origin is one GitHub
// repository, containing a submodule whose origin is another. Faking the git layer would prove
// nothing here, because what is being tested is what `git remote get-url origin` answers when it
// is run at a superproject root -- which is, confidently, the superproject.
func superproject(t *testing.T) (super, sub string) {
	t.Helper()
	root := tmuxtest.ScratchDir(t)
	inner := repo(t, filepath.Join(root, "src", "component-a"), "git@github.com:example-org/component-a.git")
	outer := repo(t, filepath.Join(root, "sample-repo"), "git@github.com:example-org/sample-repo.git")

	cmd := exec.Command("git", "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "component-a")
	cmd.Dir = outer
	cmd.Env = append(tmuxtest.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("submodule add: %v\n%s", err, out)
	}
	git(t, outer, "commit", "-qm", "add submodule")
	// The submodule was cloned from a local path, so give its checkout the origin it would
	// really have. Attribution is decided from that remote, so it has to be the honest one.
	git(t, filepath.Join(outer, "component-a"), "remote", "set-url", "origin",
		"git@github.com:example-org/component-a.git")
	return outer, filepath.Join(outer, "component-a")
}

func TestARealSuperprojectRootIsRecognised(t *testing.T) {
	super, sub := superproject(t)
	g := NewGit()

	if remote, _ := g.Remote(super); workspec.RepoFromRemote(remote) != "example-org/sample-repo" {
		t.Fatalf("superproject remote = %q, want example-org/sample-repo -- the fixture is wrong", remote)
	}
	if !g.HasSubmodules(super) {
		t.Error("superproject root: HasSubmodules = false, want true")
	}
	if g.HasSubmodules(sub) {
		t.Error("submodule dir: HasSubmodules = true, want false -- it declares no submodules of its own")
	}
}

// The board bug, end to end against real repositories: a session sitting at the superproject root
// says "opened PR #39" about work it did in a submodule. Before this, the superproject remote
// resolved that to sample-repo#39 -- a real, unrelated pull request that renders with a real title.
func TestABareMentionAtASuperprojectRootIsNotAttributedToTheSuperproject(t *testing.T) {
	super, _ := superproject(t)
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, NewGit(), f, &now)

	refs := tr.Discover(Session{
		ID:   "survey-session",
		Dir:  super,
		Text: "opened PR #39 for the survey\nhttps://github.com/example-org/component-a/pull/39\n",
	})

	var repos []string
	for _, ref := range refs {
		if ref.Kind == workspec.KindPR {
			repos = append(repos, ref.Repo+"#"+strconv.Itoa(ref.Number))
		}
	}
	if len(repos) != 1 || repos[0] != "example-org/component-a#39" {
		t.Fatalf("pull requests = %v, want exactly [example-org/component-a#39]", repos)
	}
}

// The plain case has to keep working: one repository, no submodules, cwd at its root. A bare
// mention there has exactly one repository it could mean.
func TestABareMentionInAPlainRepositoryStillResolves(t *testing.T) {
	dir := repo(t, filepath.Join(t.TempDir(), "solo"), "git@github.com:example-org/sample-repo.git")
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, NewGit(), f, &now)

	refs := tr.Discover(Session{ID: "s", Dir: dir, Text: "see PR #820 for the fix"})

	var found bool
	for _, ref := range refs {
		if ref.Kind == workspec.KindPR {
			found = true
			if ref.Repo != "example-org/sample-repo" || ref.Number != 820 {
				t.Errorf("got %s#%d, want example-org/sample-repo#820", ref.Repo, ref.Number)
			}
		}
	}
	if !found {
		t.Fatalf("no pull request found in %+v, want example-org/sample-repo#820", refs)
	}
}

// Working inside the submodule itself is unambiguous, and answers with the submodule.
func TestABareMentionInsideASubmoduleResolvesToTheSubmodule(t *testing.T) {
	_, sub := superproject(t)
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, NewGit(), f, &now)

	refs := tr.Discover(Session{ID: "s", Dir: sub, Text: "see PR #39 for the fix"})

	var found bool
	for _, ref := range refs {
		if ref.Kind == workspec.KindPR {
			found = true
			if ref.Repo != "example-org/component-a" || ref.Number != 39 {
				t.Errorf("got %s#%d, want example-org/component-a#39", ref.Repo, ref.Number)
			}
		}
	}
	if !found {
		t.Fatalf("no pull request found in %+v", refs)
	}
}

// What a bare mention at a superproject root leaves behind when nothing else names a repository:
// nothing. An empty column beats a confidently wrong one.
func TestAnUnplaceableBareMentionIsDropped(t *testing.T) {
	super, _ := superproject(t)
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, NewGit(), f, &now)

	refs := tr.Discover(Session{ID: "s", Dir: super, Text: "see PR #39 for the fix, part of ABC-135518"})

	for _, ref := range refs {
		if ref.Kind == workspec.KindPR {
			t.Errorf("kept %s#%d, want the bare mention dropped", ref.Repo, ref.Number)
		}
	}
	if len(refs) != 1 || refs[0].Identifier != "ABC-135518" {
		t.Errorf("refs = %+v, want just the ticket -- dropping a pull request must not drop tickets", refs)
	}
}

// A session that opened a pull request from a worktree sits, as far as the board can see, at the
// superproject root on main. The branch it pushed is on the pull request, and when that branch is
// Linear-shaped it names the ticket as surely as a checked-out one would.
func TestAPullRequestTheSessionOpenedLendsItsBranchTicket(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}, prs: map[string]forge.PR{
		"pr:example-org/component-b#1392": {Repo: "example-org/component-b", Number: 1392, HeadRef: "alice/abc-137108-sampleapp-derive"},
		"pr:example-org/component-b#1339": {Repo: "example-org/component-b", Number: 1339, HeadRef: "feat/no-ticket-here"},
	}}
	tr := tracker(t, fakeGit{}, f, &now)

	sess := Session{ID: "s", Text: "https://github.com/example-org/component-b/pull/1392\nsee https://github.com/example-org/component-b/pull/1339 too"}
	tr.Discover(sess)
	tr.Refresh([]Session{sess})

	var tickets []workspec.Ref
	for _, ref := range tr.For("s").Refs {
		if ref.Kind == workspec.KindTicket {
			tickets = append(tickets, ref)
		}
	}
	if len(tickets) != 1 || tickets[0].Identifier != "ABC-137108" || tickets[0].Provenance != workspec.FromBranch {
		t.Fatalf("tickets = %+v, want one ABC-137108 from the branch of the pull request the session opened", tickets)
	}
	// The mentioned pull request's branch is not evidence of anything: the session did not push it.
	for _, ref := range tr.For("s").Refs {
		if ref.Kind == workspec.KindTicket && ref.Identifier != "ABC-137108" {
			t.Errorf("unexpected ticket %+v", ref)
		}
	}
}

// The shorthand a superproject's operators write: "go#1392" is the submodule at path go, and
// "sample-repo#7585" is the superproject, from the root or from inside the submodule. Real repositories,
// because the names come out of .gitmodules and the remotes.
func TestNamesResolveSubmodulesAndTheSuperproject(t *testing.T) {
	super, sub := superproject(t)
	g := NewGit()
	for _, dir := range []string{super, sub} {
		names := g.Names(dir)
		if names["sample-repo"] != "example-org/sample-repo" {
			t.Errorf("%s: names[sample-repo] = %q, want example-org/sample-repo (%v)", dir, names["sample-repo"], names)
		}
		if names["component-a"] != "example-org/component-a" {
			t.Errorf("%s: names[component-a] = %q, want example-org/component-a (%v)", dir, names["component-a"], names)
		}
	}
}

// The site-graph session: at the sample-repo root, its screen says "go#1392 is updated". A bare number
// there is unplaceable, but the name is not -- go is a submodule with a remote.
func TestANamedMentionAtASuperprojectRootResolvesToTheNamedSubmodule(t *testing.T) {
	super, _ := superproject(t)
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, NewGit(), f, &now)

	refs := tr.Discover(Session{ID: "s", Dir: super, Text: "Done. component-a#1392 is updated with it; nothing#5 is not a repository"})

	var prs []workspec.Ref
	for _, ref := range refs {
		if ref.Kind == workspec.KindPR {
			prs = append(prs, ref)
		}
	}
	if len(prs) != 1 || prs[0].Repo != "example-org/component-a" || prs[0].Number != 1392 || prs[0].Inferred {
		t.Fatalf("got %+v, want one stated example-org/component-a#1392", prs)
	}
}

/* --------------------------------------------------------------------- staying under the quota */

// A source that has said "not before this time" is not asked before that time, whatever is due.
//
// The intervals are about how stale a reference may get. This is the other axis and it belongs to
// the source: a rate-limited GitHub is not made askable by a pull request falling due, and the tick
// that asks anyway is the one that keeps the limit alive.
func TestASourceThatAskedToWaitIsNotAsked(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{
		Reason: "GitHub rate limit reached", RetryAfter: now.Add(30 * time.Minute),
	}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394", Live: true}
	tr.Discover(session)

	tr.Refresh([]Session{session})
	if f.calls != 1 {
		t.Fatalf("calls = %d, want the first look to happen", f.calls)
	}

	// Well past the reference's own interval, and still inside the wait the source asked for.
	for i := 0; i < 5; i++ {
		now = now.Add(LiveInterval * 2)
		tr.Refresh([]Session{session})
	}
	if f.calls != 1 {
		t.Errorf("asked %d times inside the wait GitHub asked for, want 1", f.calls)
	}

	now = now.Add(30 * time.Minute)
	f.health = forge.Health{OK: true}
	tr.Refresh([]Session{session})
	if f.calls != 2 {
		t.Errorf("calls = %d, want the source asked again once the wait was over", f.calls)
	}
}

// One source standing down must not cost the board the other. GitHub and Linear have separate
// quotas and separate credentials, and a rate-limited GitHub says nothing about Linear.
func TestOneSourceStandingDownDoesNotSilenceTheOther(t *testing.T) {
	now := time.Unix(1700000000, 0)
	healthy := forge.Health{OK: true}
	f := &fakeForge{
		health: forge.Health{
			Reason: "GitHub rate limit reached", RetryAfter: now.Add(time.Hour),
		},
		ticketHealth: &healthy,
	}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394 for ABC-133756", Live: true}
	tr.Discover(session)
	tr.Refresh([]Session{session})

	before := f.ticketCalls
	now = now.Add(LiveInterval * 2)
	tr.Refresh([]Session{session})
	if f.ticketCalls == before {
		t.Error("Linear was not asked because GitHub was rate limited")
	}
}

// A look that went partly wrong still settles what it answered for. Without this, one reference
// naming a repository that does not exist puts the whole board back on the queue every tick —
// which is the loop that exhausts a quota in the first place.
func TestAPartialAnswerSettlesOnlyWhatItAnswered(t *testing.T) {
	now := time.Unix(1700000000, 0)
	good := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: 7394}
	f := &fakeForge{health: forge.Health{
		Reason:   "something went wrong for part of the batch",
		Answered: map[string]bool{good.Key(): true},
	}}
	tr := tracker(t, fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	session := Session{ID: "s", Dir: "/repo", Text: "PR #7394 and PR #7395", Live: true}
	tr.Discover(session)
	tr.Refresh([]Session{session})

	now = now.Add(LiveInterval / 2)
	tr.Refresh([]Session{session})
	if f.calls != 2 {
		t.Fatalf("calls = %d, want the unanswered reference asked again", f.calls)
	}
	asked := f.asked[len(f.asked)-1]
	if len(asked) == 0 {
		t.Fatal("the reference that was never answered was not asked about again")
	}
	for _, ref := range asked {
		if ref.Key() == good.Key() {
			t.Error("a reference that was answered was asked about again inside its interval")
		}
	}
}
