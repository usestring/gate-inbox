// Package worktracker answers "what is this session working on, and what is it doing now?".
//
// Discovery is local and cheap: a git branch, a remote, and text the session already wrote. What
// costs something is asking GitHub and Linear, so that happens on its own ticker and never on a
// render path. The board draws whatever was last resolved.
//
// The rule that shapes the whole package is that being late is fine and being wrong is not. A
// stale pull request state is obvious to a reader and self-corrects on the next tick; a confident
// wrong one — the wrong repository, a ticket key that was really a serial number — is indefinitely
// misleading and looks exactly like a right one.
package worktracker

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
	"github.com/usestring/gate-inbox/internal/workspec"
)

// Refresh intervals. The sweep runs every 1.2s; nothing here may ride on it.
//
// Four tiers rather than two, because the quota is finite and the operator is looking at one thing.
// What is on screen should be right now; what is merely running can be a minute behind; what ended
// yesterday can be ten minutes behind; and what is finished need barely be checked at all.
const (
	// VisibleInterval covers what the operator is looking at. Shorter than the work tick, so a
	// visible row is re-read every tick and a check going red is seen as it happens.
	VisibleInterval = 20 * time.Second
	// LiveInterval covers a session that is still open but not on screen.
	LiveInterval = 60 * time.Second
	// IdleInterval covers everything else. A pull request on a session that ended yesterday
	// changes slowly and nobody is watching it.
	IdleInterval = 10 * time.Minute
	// TerminalInterval covers a pull request that is merged or closed.
	//
	// It is not going to change, and the board accumulates these forever: every reference any
	// session ever mentioned stays on it while that session is around, so on a busy machine
	// most of what falls due is work that finished days ago. Re-reading them at the idle
	// interval was quietly the largest standing cost here. Not never, because a closed pull
	// request can be reopened, and a stale "closed" is cheap to carry for a few hours.
	TerminalInterval = 6 * time.Hour
)

// DefaultSettleAfter is how long a settled artifact stays on the board after
// the operator first had it on screen settled. A pull request is interesting
// the day it merges; the day after, it is history the tracker can stop
// drawing.
const DefaultSettleAfter = 24 * time.Hour

// SeenStore keeps the sightings across restarts. The clock on a settled
// artifact runs from the first frame that showed it settled, and a restart
// that lost every sighting would put the whole settled backlog back for
// another day.
type SeenStore interface {
	WorkSeen() (map[string]time.Time, error)
	RecordWorkSeen(map[string]time.Time) error
	ForgetWorkSeen([]string) error
}

// RefreshBudget is the most references one source is asked about in a single pass.
//
// A batch's GraphQL point cost scales with how many pull requests are in it -- each one carries a
// hundred check contexts -- so an unbounded batch is an unbounded spend, and a board that has been
// idle for an hour comes back with everything due at once. Capping the pass turns that spike into a
// few ordinary passes thirty seconds apart, and because due() hands back the visible references
// first, the ones the operator is actually looking at are in the first pass rather than the fourth.
//
// Per source rather than shared: GitHub and Linear have separate quotas, so a board thick with pull
// requests must not starve its tickets.
const RefreshBudget = 40

// Session is the little the tracker needs to know about one.
type Session struct {
	ID string
	// Dir is the working directory, which is where the branch and remote come from.
	Dir string
	// Text is everything the session and its operator wrote, for the reference scan: the
	// launch prompt, the screen, and the mention lines the search index kept from the
	// transcript.
	Text string
	Live bool
	// Visible marks a session the operator is looking at right now.
	//
	// The caller decides what that means, because only the view knows what it drew. The
	// tracker only needs the answer to be roughly right: being one row out changes which of
	// two references is re-read thirty seconds sooner.
	Visible bool
}

// Work is what one session is working on, with whatever live state has been resolved.
type Work struct {
	SessionID string
	Refs      []workspec.Ref
	PRs       map[string]forge.PR
	Tickets   map[string]forge.Ticket
	// Retired marks the references the board no longer draws: settled ones --
	// a merged or closed pull request, a completed or cancelled ticket -- that
	// have been on screen settled for SettleAfter. Kept per session rather
	// than dropped from Refs so a view that wants the whole history can still
	// have it.
	Retired map[string]bool
	// Looked marks the references a source has already answered for.
	//
	// Resolving to nothing is a real answer, and a common one: workspec's
	// ticket pattern matches "CVE-2024" as readily as "ABC-100001". So a
	// reference that is neither resolved nor looked at is one nobody has
	// asked about yet, which is a different thing entirely from one the
	// source says does not exist -- the same distinction Health draws for the
	// sources as a whole, drawn here per reference.
	Looked map[string]bool
}

// NeedsYou reports whether anything this session produced is waiting on a person.
func (w Work) NeedsYou() bool {
	for _, pr := range w.PRs {
		if pr.NeedsYou() {
			return true
		}
	}
	return false
}

// Git is the local repository questions discovery asks.
type Git interface {
	Branch(dir string) (string, bool)
	Remote(dir string) (string, bool)
	// HasSubmodules reports whether the repository containing dir is a superproject.
	HasSubmodules(dir string) bool
	// Names is every repository dir can see by a short name, lowercased: the
	// repository itself, the superproject above it, and each submodule path
	// the superproject declares, each mapped to "owner/name".
	Names(dir string) map[string]string
}

// gitCommands answers the local questions by forking git, and remembers what
// each working directory answered.
//
// The cache is here rather than in the tracker because the forks are. The board
// runs dozens of sessions over a handful of distinct directories -- forty-seven
// sessions across twenty-two directories in an hour on this machine, ten of
// which account for nearly all of it -- and every session on a directory asked
// it the same four questions on every tick. Measured on the live board that was
// thirty-two thousand git processes an hour, all but a few hundred of them
// re-asking something already answered.
type gitCommands struct {
	mu    sync.Mutex
	facts map[string]localFacts
}

func newGitCommands() *gitCommands { return &gitCommands{facts: map[string]localFacts{}} }

// NewGit reads the working directory with git itself.
func NewGit() Git { return newGitCommands() }

// LocalInterval is how long a working directory's forked answers are believed
// while nothing has visibly moved.
//
// The same standing NamesInterval has, and for the same reason: a remote and a
// .gitmodules change about never. The branch is the one thing here that changes
// while somebody is working, and it is not held to this interval at all -- a
// rewritten HEAD drops the entry on the spot, so a checkout reaches the board on
// the next tick rather than up to ten minutes later.
const LocalInterval = 10 * time.Minute

// localFacts is everything discovery asks about one working directory, beside
// what makes those answers still true.
type localFacts struct {
	branch, remote       string
	hasBranch, hasRemote bool
	submodules           bool

	// gitDir is where to find the checkout's HEAD again, and head is that file's
	// bytes as they stood when the forks ran. git rewrites HEAD on every checkout
	// and switch, so bytes that have not moved mean the branch above is still the
	// branch -- and comparing the bytes rather than reading them is what keeps
	// this a cache check instead of a second, worse, implementation of rev-parse.
	//
	// hasHead is false for a directory that is not a repository at all, which has
	// no HEAD to watch. About a third of the directories on this board are that:
	// a path belonging to a session inside a container, which does not exist out
	// here and never will.
	gitDir, head string
	hasHead      bool

	at time.Time
}

// factsFor is the four questions, forked at most once per directory per
// checkout.
func (g *gitCommands) factsFor(dir string) localFacts {
	now := time.Now()
	g.mu.Lock()
	cached, ok := g.facts[dir]
	g.mu.Unlock()
	if ok && now.Sub(cached.at) < LocalInterval && cached.stillHolds() {
		return cached
	}
	fresh := g.resolve(dir, now)
	g.mu.Lock()
	g.facts[dir] = fresh
	g.evictLocked(now)
	g.mu.Unlock()
	return fresh
}

// stillHolds reports whether the checkout has moved under the cached answers.
//
// A directory with no HEAD to watch is held to the interval alone. What that
// costs is a session in a directory that has just become a repository showing no
// work for a few minutes; what the alternative costs is re-forking three
// processes a tick, forever, for the directories that are not repositories.
func (f localFacts) stillHolds() bool {
	if !f.hasHead {
		return true
	}
	head, ok := readHead(f.gitDir)
	return ok && head == f.head
}

// resolve forks for one directory. The order is load-bearing: HEAD is read
// before the branch fork rather than after it, so a checkout landing in the
// middle leaves an entry whose HEAD no longer matches and which the next tick
// re-forks. The other order would pair the new HEAD with the old branch and
// then believe it for the whole interval.
func (g *gitCommands) resolve(dir string, now time.Time) localFacts {
	fresh := localFacts{at: now}
	// One fork for both roots: .gitmodules lives at the top level and HEAD lives
	// in the git directory, and rev-parse prints each option it is given on its
	// own line.
	if roots, ok := g.run(dir, "rev-parse", "--show-toplevel", "--absolute-git-dir"); ok {
		top, gitDir, split := strings.Cut(roots, "\n")
		if split {
			fresh.gitDir = gitDir
			fresh.head, fresh.hasHead = readHead(gitDir)
		}
		_, fresh.submodules = g.run(dir, "config", "--file", filepath.Join(top, ".gitmodules"), "--get-regexp", `^submodule\..*\.path$`)
	}
	fresh.branch, fresh.hasBranch = g.run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	fresh.remote, fresh.hasRemote = g.run(dir, "remote", "get-url", "origin")
	return fresh
}

// readHead is the checkout's HEAD pointer: a thirty-byte read of a file the page
// cache already holds, in place of a process.
func readHead(gitDir string) (string, bool) {
	if gitDir == "" {
		return "", false
	}
	blob, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", false
	}
	return string(blob), true
}

// evictLocked drops directories nothing has asked about for long enough that the
// answer would be forked afresh anyway. Without it the map keeps an entry for
// every directory the board ever saw, and Names alone adds one per submodule of
// every superproject on it.
func (g *gitCommands) evictLocked(now time.Time) {
	for dir, cached := range g.facts {
		if now.Sub(cached.at) > 2*LocalInterval {
			delete(g.facts, dir)
		}
	}
}

// run forks git once. Discovery is described as cheap, and one of these is;
// what the board actually spends is the number of them, so each one is a span
// rather than the discovery that asked for it. The subcommand and the working
// directory are what separate them: this fleet runs dozens of sessions over a
// handful of distinct directories, and a trace that says so is the evidence
// for caching the answer rather than a suspicion.
func (*gitCommands) run(dir string, args ...string) (string, bool) {
	traced := tracing.Enabled()
	var started time.Time
	if traced {
		started = time.Now()
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if traced {
		subcommand := ""
		if len(args) > 0 {
			subcommand = args[0]
		}
		tracing.Record("worktracker.git", started, time.Now(), err,
			tracing.Attr{Key: "git.subcommand", Value: subcommand},
			tracing.Attr{Key: "dir", Value: dir})
	}
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(out))
	return value, value != ""
}

func (g *gitCommands) Branch(dir string) (string, bool) {
	facts := g.factsFor(dir)
	return facts.branch, facts.hasBranch
}

func (g *gitCommands) Remote(dir string) (string, bool) {
	facts := g.factsFor(dir)
	return facts.remote, facts.hasRemote
}

// HasSubmodules reports whether the repository containing dir declares any submodule.
//
// It reads .gitmodules at the top level rather than asking `git submodule status`, because a
// submodule that was never initialised still makes the working directory ambiguous and still
// appears in the file. Anything that goes wrong -- no repository, no .gitmodules, an unreadable
// one -- answers false, which is the ordinary single-repository case.
func (g *gitCommands) HasSubmodules(dir string) bool { return g.factsFor(dir).submodules }

// remoteOf is the origin URL with nothing cached around it.
//
// Names asks this of every submodule path a superproject declares -- twenty of
// them on this board -- and those are paths no session sits in. Giving each one
// a cache entry would fork four processes to save one, and Names itself is
// already held for NamesInterval by the tracker.
func (g *gitCommands) remoteOf(dir string) (string, bool) {
	return g.run(dir, "remote", "get-url", "origin")
}

// Names reads the short names the operator writes a pull request under. At a
// superproject root "go#1392" means the submodule at path go; inside that
// submodule it means the same thing, and "sample-repo#7585" means the superproject.
// A plain repository knows only its own name.
func (g *gitCommands) Names(dir string) map[string]string {
	names := map[string]string{}
	add := func(name, remote string) {
		if repo := workspec.RepoFromRemote(remote); repo != "" && name != "" {
			names[strings.ToLower(name)] = repo
		}
	}
	if remote, ok := g.remoteOf(dir); ok {
		add(path.Base(workspec.RepoFromRemote(remote)), remote)
	}
	top, ok := g.run(dir, "rev-parse", "--show-toplevel")
	if !ok {
		return names
	}
	if super, ok := g.run(dir, "rev-parse", "--show-superproject-working-tree"); ok {
		top = super
		if remote, ok := g.remoteOf(super); ok {
			add(path.Base(workspec.RepoFromRemote(remote)), remote)
		}
	}
	urls, ok := g.run(top, "config", "--file", filepath.Join(top, ".gitmodules"), "--get-regexp", `^submodule\..*\.url$`)
	if !ok {
		return names
	}
	for _, line := range strings.Split(urls, "\n") {
		key, url, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".url")
		if p, ok := g.run(top, "config", "--file", filepath.Join(top, ".gitmodules"), "--get", "submodule."+name+".path"); ok {
			name = p
		}
		// The checkout's own remote first: a submodule added from a local
		// clone records that path in .gitmodules, and the remote is where the
		// pull requests really are.
		if remote, ok := g.remoteOf(filepath.Join(top, name)); ok {
			url = remote
		}
		add(name, url)
	}
	return names
}

// Memory is where resolved state survives a restart.
//
// An interface rather than the store itself, for the same reason Git and the two resolvers are:
// this package's tests describe what was remembered without standing up a database, and a tracker
// with no Memory at all is the ordinary in-process case rather than a special one.
type Memory interface {
	ForgeState(now time.Time) ([]store.StoredPR, []store.StoredTicket, error)
	SaveForgeState(prs []store.StoredPR, tickets []store.StoredTicket) error
}

// Tracker discovers and resolves work across sessions.
type Tracker struct {
	Git Git
	// GitHub and Linear are nil for a provider that is off. The tracker then keeps no reference
	// of that kind, so nothing of it is asked about, remembered or drawn.
	GitHub forge.PRResolver
	Linear forge.TicketResolver
	// Memory is optional. Without it the tracker behaves exactly as it did before there was
	// one: everything is asked about from cold on every start.
	Memory Memory
	Now    func() time.Time
	// SettleAfter is how long a settled artifact outlives its first sighting
	// on screen. Zero means DefaultSettleAfter.
	SettleAfter time.Duration

	mu      sync.RWMutex
	refs    map[string][]workspec.Ref
	prs     map[string]forge.PR
	tickets map[string]forge.Ticket
	fetched map[string]time.Time
	health  struct{ gh, linear forge.Health }
	// seen is when each settled artifact was first on screen. dirty and
	// dropped are what Refresh has yet to write through to the store: the
	// marks arrive off the event loop, where a database write does not
	// belong, and Refresh already runs on its own goroutine.
	seen    map[string]time.Time
	dirty   map[string]time.Time
	dropped []string
	store   SeenStore

	// quiet is when each source is next worth asking, from the RetryAfter it named.
	//
	// The intervals above decide how often a *reference* is worth re-reading. This is the
	// other axis and it belongs to the source: a rate-limited GitHub is not made askable by a
	// reference falling due, and asking anyway is what deepens the hole.
	quiet struct{ gh, linear time.Time }
	// names caches Git.Names per working directory. Reading it is a git
	// call per submodule, and a superproject on this board declares twenty;
	// .gitmodules and remotes change about never, so once a while is plenty.
	names map[string]namedRepos
}

type namedRepos struct {
	repos map[string]string
	at    time.Time
}

// NamesInterval is how long a working directory's short names are believed.
const NamesInterval = 10 * time.Minute

func New(git Git, gh forge.PRResolver, linear forge.TicketResolver) *Tracker {
	return &Tracker{
		Git: git, GitHub: gh, Linear: linear, Now: time.Now,
		refs:    map[string][]workspec.Ref{},
		prs:     map[string]forge.PR{},
		tickets: map[string]forge.Ticket{},
		fetched: map[string]time.Time{},
		names:   map[string]namedRepos{},
		seen:    map[string]time.Time{},
		dirty:   map[string]time.Time{},
	}
}

// WithSeen gives the tracker somewhere to keep its sightings, and reads back
// the ones already there. A store that cannot be read leaves the tracker
// running on memory alone, which costs one day of settled rows after a
// restart and nothing else.
func (t *Tracker) WithSeen(store SeenStore) *Tracker {
	seen, err := store.WorkSeen()
	if err != nil {
		seen = map[string]time.Time{}
	}
	t.mu.Lock()
	t.store = store
	for key, at := range seen {
		t.seen[key] = at
	}
	t.mu.Unlock()
	return t
}

// MarkSeen records that these references were on screen, for the ones that
// are settled. The first sighting is the one that counts; later frames
// drawing the same row do not move the clock. A reference that is not
// settled, or not resolved at all, is left alone: the clock is for things
// that are over, and nothing else may be aged off the board.
func (t *Tracker) MarkSeen(keys []string) {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, key := range keys {
		if _, ok := t.seen[key]; ok || !t.doneLocked(key) {
			continue
		}
		t.seen[key] = now
		t.dirty[key] = now
	}
}

// doneLocked reports whether a resolved reference is settled.
func (t *Tracker) doneLocked(key string) bool {
	if pr, ok := t.prs[key]; ok {
		return pr.Done()
	}
	if ticket, ok := t.tickets[key]; ok {
		return ticket.Done()
	}
	return false
}

// retiredLocked reports whether a reference has been settled and on screen
// for long enough to leave the board.
func (t *Tracker) retiredLocked(key string, now time.Time) bool {
	seen, ok := t.seen[key]
	if !ok || !t.doneLocked(key) {
		return false
	}
	after := t.SettleAfter
	if after <= 0 {
		after = DefaultSettleAfter
	}
	return now.Sub(seen) >= after
}

// forgetSeenLocked drops a sighting, in memory and on the next flush.
func (t *Tracker) forgetSeenLocked(key string) {
	if _, ok := t.seen[key]; !ok {
		return
	}
	delete(t.seen, key)
	delete(t.dirty, key)
	t.dropped = append(t.dropped, key)
}

// flushSeen writes the sightings marked and dropped since the last flush.
// Called from Refresh, off the event loop. A write that fails is retried on
// the next pass, the same standing the fetch itself has.
func (t *Tracker) flushSeen() {
	t.mu.Lock()
	store, dirty, dropped := t.store, t.dirty, t.dropped
	t.dirty, t.dropped = map[string]time.Time{}, nil
	t.mu.Unlock()
	if store == nil {
		return
	}
	requeue := func() {
		t.mu.Lock()
		t.dropped = append(t.dropped, dropped...)
		for key, at := range dirty {
			if _, ok := t.dirty[key]; !ok {
				t.dirty[key] = at
			}
		}
		t.mu.Unlock()
	}
	// The drops go first: a key dropped and marked again since the last
	// flush must lose its old row before the new one is written, or the
	// store keeps the old clock. A failed drop therefore holds the marks
	// back too, rather than writing them against rows that are still there.
	if err := store.ForgetWorkSeen(dropped); err != nil {
		requeue()
		return
	}
	if err := store.RecordWorkSeen(dirty); err != nil {
		dropped = nil
		requeue()
	}
}

// Restore seeds the cache from the last run, without making any of it current.
//
// The rows land in prs and tickets but deliberately not in fetched, which is what marks a reference
// as settled. So every restored reference is due on the first tick and re-verified in the ordinary
// way, and until it is, Work.Looked reports false for it: the board draws a remembered value with
// the time it was taken rather than a live one. The whole benefit is that the first tick after a
// restart is a normal refresh instead of the largest burst of quota the inbox ever spends, and the
// work column has something in it while that happens.
//
// A failure here is not worth stopping for. The board simply starts cold, which is what it did
// before this existed.
func (t *Tracker) Restore() error {
	if t.Memory == nil {
		return nil
	}
	prs, tickets, err := t.Memory.ForgeState(t.now())
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	// A provider switched off since the last run leaves its rows in the store, untouched for
	// when it is switched back on, but nothing of it is drawn now.
	if !t.enabled(workspec.KindPR) {
		prs = nil
	}
	if !t.enabled(workspec.KindTicket) {
		tickets = nil
	}
	for _, pr := range prs {
		t.prs[pr.Key] = forge.PR{
			Repo: pr.Repo, Number: pr.Number, Title: pr.Title,
			State: forge.PRState(pr.State), Checks: forge.ChecksState(pr.Checks),
			Review: forge.ReviewState(pr.Review), URL: pr.URL, Mergeable: pr.Mergeable,
			HeadRef: pr.HeadRef, FailingChecks: pr.FailingChecks, FetchedAt: pr.FetchedAt,
		}
	}
	for _, ticket := range tickets {
		t.tickets[ticket.Key] = forge.Ticket{
			Identifier: ticket.Identifier, Title: ticket.Title, State: ticket.State,
			StateType: ticket.StateType, Assignee: ticket.Assignee, URL: ticket.URL,
			FetchedAt: ticket.FetchedAt,
		}
	}
	return nil
}

// remember writes back what a refresh resolved.
//
// Only what this run actually confirmed: the argument is the freshly resolved maps rather than the
// whole cache, so a restored row nobody has verified is never written back with a new timestamp.
// Doing that would let a value survive indefinitely by being repeatedly re-saved without ever being
// checked, which is exactly the confidently-wrong state the unverified marking exists to prevent.
func (t *Tracker) remember(prs map[string]forge.PR, tickets map[string]forge.Ticket) {
	if t.Memory == nil || (len(prs) == 0 && len(tickets) == 0) {
		return
	}
	stored := make([]store.StoredPR, 0, len(prs))
	for key, pr := range prs {
		stored = append(stored, store.StoredPR{
			Key: key, Repo: pr.Repo, Number: pr.Number, Title: pr.Title,
			State: string(pr.State), Checks: string(pr.Checks), Review: string(pr.Review),
			URL: pr.URL, Mergeable: pr.Mergeable, HeadRef: pr.HeadRef,
			FailingChecks: pr.FailingChecks, FetchedAt: pr.FetchedAt,
		})
	}
	storedTickets := make([]store.StoredTicket, 0, len(tickets))
	for key, ticket := range tickets {
		storedTickets = append(storedTickets, store.StoredTicket{
			Key: key, Identifier: ticket.Identifier, Title: ticket.Title, State: ticket.State,
			StateType: ticket.StateType, Assignee: ticket.Assignee, URL: ticket.URL,
			FetchedAt: ticket.FetchedAt,
		})
	}
	if err := t.Memory.SaveForgeState(stored, storedTickets); err != nil {
		logging.Warn("save forge state", logging.Err(err))
	}
}

func (t *Tracker) namesFor(dir string) map[string]string {
	now := t.now()
	t.mu.RLock()
	cached, ok := t.names[dir]
	t.mu.RUnlock()
	if ok && now.Sub(cached.at) < NamesInterval {
		return cached.repos
	}
	repos := t.Git.Names(dir)
	t.mu.Lock()
	t.names[dir] = namedRepos{repos: repos, at: now}
	t.mu.Unlock()
	return repos
}

// enabled reports whether the provider answering for this kind is switched on.
func (t *Tracker) enabled(kind workspec.Kind) bool {
	switch kind {
	case workspec.KindPR:
		return t.GitHub != nil
	case workspec.KindTicket:
		return t.Linear != nil
	}
	return false
}

// keyEnabled is enabled for a reference key, which carries its kind as a prefix.
func (t *Tracker) keyEnabled(key string) bool {
	kind, _, _ := strings.Cut(key, ":")
	return t.enabled(workspec.Kind(kind))
}

// enabledRefs drops the references no switched-on provider answers for.
func (t *Tracker) enabledRefs(refs []workspec.Ref) []workspec.Ref {
	kept := refs[:0]
	for _, ref := range refs {
		if t.enabled(ref.Kind) {
			kept = append(kept, ref)
		}
	}
	return kept
}

func (t *Tracker) now() time.Time {
	if t.Now == nil {
		return time.Now()
	}
	return t.Now()
}

// Discover works out what a session is working on, without touching the network.
//
// Three sources, strongest first. The branch is the strongest by a distance: somebody checked it
// out to do the work, which no amount of talking about a ticket implies. Then the session watching
// itself open a pull request. Then anything it merely said.
func (t *Tracker) Discover(session Session) []workspec.Ref {
	// The whole of one session's local discovery, which is where the git
	// forks above come from: the span says how many a session cost by
	// bracketing them, and how much of the board's tick one session is. Most
	// bracket no forks at all, the directory having been read for an earlier
	// session; the ones that do are a directory nobody has asked about lately,
	// or a checkout that has moved.
	// Neither the session's text nor anything scanned out of it goes on it --
	// that text is the pane and the transcript, and a trace leaves the box.
	traced := tracing.Enabled()
	var started time.Time
	if traced {
		started = time.Now()
	}
	var refs []workspec.Ref
	repo := ""

	if session.Dir != "" && t.Git != nil {
		if remote, ok := t.Git.Remote(session.Dir); ok {
			repo = workspec.RepoFromRemote(remote)
		}
		// A superproject checkout cannot resolve a bare "PR #39".
		//
		// A pane sitting at a superproject root is usually doing the work in one of its
		// submodules, and `git remote get-url origin` there answers for the superproject.
		// The danger is that the wrong answer resolves: the superproject has a #39 too, so
		// the board renders an unrelated pull request with a real title and a real state,
		// indistinguishable from the right one. Unknown restores the policy ScanText
		// states, which is to drop a mention it cannot place. References that name their
		// repository are unaffected, and those are what the submodule work leaves behind.
		if repo != "" && t.Git.HasSubmodules(session.Dir) {
			repo = ""
		}
		if branch, ok := t.Git.Branch(session.Dir); ok {
			if ticket, found := workspec.TicketFromBranch(branch); found {
				refs = append(refs, ticket)
			}
		}
	}

	// A pull request the session opened names its own URL in the result, so those come out of
	// the same scan as the mentions and are told apart by provenance.
	refs = append(refs, creationRefs(session.Text)...)
	refs = append(refs, workspec.ScanText(session.Text, repo, workspec.FromText)...)
	// "go#1392" at a superproject root names the submodule at go, which is
	// exactly the mention the bare-number rule above cannot place.
	if session.Dir != "" && t.Git != nil {
		refs = append(refs, workspec.ScanNamed(session.Text, t.namesFor(session.Dir), workspec.FromText)...)
	}

	refs = t.enabledRefs(workspec.Dedupe(workspec.PruneInferred(refs)))

	t.mu.Lock()
	t.refs[session.ID] = refs
	t.mu.Unlock()

	if traced {
		tracing.Record("worktracker.discover", started, time.Now(), nil,
			tracing.Attr{Key: "session", Value: session.ID},
			tracing.Attr{Key: "dir", Value: session.Dir},
			tracing.Attr{Key: "refs", Value: len(refs)},
			tracing.Attr{Key: "live", Value: session.Live})
	}
	return refs
}

// creationRefs finds pull requests the session watched itself open.
//
// `gh pr create` prints the URL of what it made, so a line that is just that URL, in output rather
// than prose, is the session reporting its own work. Stronger evidence than a mention and weaker
// than the branch, which is exactly where it sorts.
func creationRefs(text string) []workspec.Ref {
	var refs []workspec.Ref
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "https://github.com/") || strings.ContainsAny(trimmed, " \t") {
			continue
		}
		refs = append(refs, workspec.ScanText(trimmed, "", workspec.FromCreation)...)
	}
	return refs
}

// refreshSpans is what one Refresh will report, filled in as the pass runs.
//
// A collected struct rather than a trace opened at the top, because
// tracing.NewTrace wants the root's end time and a pass does not know it until
// it is over. The two sources hang off the root as children: they are the only
// parts of a refresh that leave the machine, and which of the two a slow pass
// was waiting on is the first thing anyone asks. The two overlap, because the
// pass runs them together: a root much longer than the longer child is the pass
// paying for something other than the network.
type refreshSpans struct {
	on               bool
	start            time.Time
	sessions, due    int
	ghStart, ghEnd   time.Time
	ghResolved       int
	ghOK             bool
	linStart, linEnd time.Time
	linResolved      int
	linOK            bool
}

// mark timestamps one boundary, and nothing at all on an untraced pass.
func (p *refreshSpans) mark(at *time.Time) {
	if !p.on {
		return
	}
	*at = time.Now()
}

func (p *refreshSpans) emit() {
	trace := tracing.NewTrace("worktracker.refresh", p.start, time.Now(),
		tracing.Attr{Key: "sessions", Value: p.sessions},
		tracing.Attr{Key: "due", Value: p.due})
	if !p.ghEnd.IsZero() {
		trace.Child("worktracker.github", p.ghStart, p.ghEnd,
			tracing.Attr{Key: "resolved", Value: p.ghResolved},
			tracing.Attr{Key: "ok", Value: p.ghOK})
	}
	if !p.linEnd.IsZero() {
		trace.Child("worktracker.linear", p.linStart, p.linEnd,
			tracing.Attr{Key: "resolved", Value: p.linResolved},
			tracing.Attr{Key: "ok", Value: p.linOK})
	}
	trace.Emit()
}

// Refresh resolves live state for everything discovered, in two batched requests, and forgets
// everything belonging to sessions that are no longer on the board.
//
// Called from a ticker, never from a render. Failure leaves the previous answers in place: a board
// that blanks its work column because GitHub had a bad minute is worse than one showing a minute-old
// truth, and the health states say plainly that the last look failed.
//
// sessions is the whole board, not a selection. Both halves depend on that: due reads it to decide
// what to ask about, and forget reads it to decide what to keep. Handing this a subset would drop
// the state of every session left out of it.
func (t *Tracker) Refresh(sessions []Session) {
	// Declared before the defer that emits it and registered before every
	// other defer here, so the pass's span closes after the store writes the
	// others do. Those writes are part of what a refresh costs, and a span
	// that stopped at the last statement would report a pass that ends before
	// it has finished paying.
	var pass refreshSpans
	if tracing.Enabled() {
		pass.on = true
		pass.start = time.Now()
		pass.sessions = len(sessions)
		defer func() { pass.emit() }()
	}

	// Before due, not after: a pass with nothing due returns early, and a
	// board that has gone quiet is exactly when the sessions that ended are
	// piling up.
	t.forget(sessions)
	defer t.flushSeen()

	due := t.due(sessions)
	pass.due = len(due)
	if len(due) == 0 {
		return
	}

	// A source that asked to be left alone is not asked, whatever is due. Each is decided
	// separately: a rate-limited GitHub must not also cost the board its ticket states.
	askGitHub, askLinear := t.askable()

	var (
		prs       map[string]forge.PR
		tickets   map[string]forge.Ticket
		ghHealth  forge.Health
		linHealth forge.Health
		lookedGH  bool
		lookedLin bool
	)
	// Both at once. Two services, two connections, two quotas, and neither
	// one's answer is an input to the other, so asking them in turn costs the
	// board the sum of two waits for nothing: measured on the live board, a
	// median pass of 1489ms over a 908ms GitHub and a 445ms Linear, and a worst
	// pass of 11168ms that was a Linear timeout with a GitHub queued behind it.
	//
	// Each goroutine writes only its own variables and its own half of the span
	// struct, which is what keeps this free of a lock; everything that merges
	// them into the tracker happens after the wait.
	var sources sync.WaitGroup
	if askGitHub && t.GitHub != nil && anyOfKind(due, workspec.KindPR) {
		lookedGH = true
		sources.Add(1)
		go func() {
			defer sources.Done()
			pass.mark(&pass.ghStart)
			prs, ghHealth = t.GitHub.PRs(due)
			pass.mark(&pass.ghEnd)
			pass.ghResolved, pass.ghOK = len(prs), ghHealth.OK
		}()
	}
	if askLinear && t.Linear != nil && anyOfKind(due, workspec.KindTicket) {
		lookedLin = true
		sources.Add(1)
		go func() {
			defer sources.Done()
			pass.mark(&pass.linStart)
			tickets, linHealth = t.Linear.Tickets(due)
			pass.mark(&pass.linEnd)
			pass.linResolved, pass.linOK = len(tickets), linHealth.OK
		}()
	}
	sources.Wait()

	// Outside the lock: this writes to SQLite, and holding the tracker's lock across a disk
	// write would stall every render that reads it. Only what was resolved this pass goes in.
	defer t.remember(prs, tickets)

	t.mu.Lock()
	defer t.mu.Unlock()

	at := t.now()
	if lookedGH {
		t.health.gh = ghHealth
		t.quiet.gh = ghHealth.RetryAfter
	}
	if lookedLin {
		t.health.linear = linHealth
		t.quiet.linear = linHealth.RetryAfter
	}

	// A sighting is of a settled artifact. One that comes back open -- a
	// reopened pull request, a ticket moved back to started -- is live work
	// again, and its clock must start over from the next time it settles.
	for key, pr := range prs {
		t.prs[key] = pr
		if !pr.Done() {
			t.forgetSeenLocked(key)
		}
	}
	for key, ticket := range tickets {
		t.tickets[key] = ticket
		if !ticket.Done() {
			t.forgetSeenLocked(key)
		}
	}
	t.adoptBranchTickets()

	// Stamp everything a source *settled*, not just what came back and not only when the whole
	// look succeeded.
	//
	// A reference can resolve to nothing perfectly legitimately: workspec's ticket pattern
	// matches anything shaped like ABC-123, and plenty of those name no ticket. Stamping only
	// what was found would leave those permanently due, so every tick would ask again — the
	// exact hot loop the interval exists to prevent, aimed at references that will never
	// resolve.
	//
	// Health.Answered is that set, and it is per reference rather than per batch for the same
	// reason: one reference naming a repository that does not exist makes gh exit non-zero for
	// the whole request, and the thirty pull requests answered in that same response are
	// settled regardless. What is genuinely unanswered — a page that never went out, a look
	// that failed outright — is simply not in it, and comes due again as before.
	t.stamp(due, workspec.KindPR, lookedGH, ghHealth, at)
	t.stamp(due, workspec.KindTicket, lookedLin, linHealth, at)
}

// stamp records what one source settled in this look.
//
// A healthy look that named nothing settled everything it was asked: that is the plain reading of
// "the request succeeded", and it keeps a resolver that does not populate Answered — a test double,
// a future third source — from silently falling back into asking about everything on every tick.
// Answered is therefore additive: it is how a look that went *partly* wrong says which references
// it nevertheless settled.
func (t *Tracker) stamp(due []workspec.Ref, kind workspec.Kind, looked bool, health forge.Health, at time.Time) {
	if !looked {
		return
	}
	if health.Answered == nil {
		if !health.OK {
			return
		}
		for _, ref := range due {
			if ref.Kind == kind {
				t.fetched[ref.Key()] = at
			}
		}
		return
	}
	for key := range health.Answered {
		t.fetched[key] = at
	}
}

// askable reports which sources are past the quiet period they asked for.
func (t *Tracker) askable() (github, linear bool) {
	now := t.now()
	t.mu.RLock()
	defer t.mu.RUnlock()
	return !now.Before(t.quiet.gh), !now.Before(t.quiet.linear)
}

func anyOfKind(refs []workspec.Ref, kind workspec.Kind) bool {
	for _, ref := range refs {
		if ref.Kind == kind {
			return true
		}
	}
	return false
}

// adoptBranchTickets gives every session the ticket named by the head branch
// of a pull request it opened.
//
// A session that opens a PR from a worktree has a working directory the board
// reads as the superproject on main, so TicketFromBranch sees nothing there;
// the branch it actually pushed is on the pull request. That branch is the
// same evidence as a checked-out one -- somebody named the work after the
// ticket -- so it ranks the same. Runs under the write lock, after the pull
// requests have been resolved, and the tickets it adds are due on the next
// tick like any other reference.
func (t *Tracker) adoptBranchTickets() {
	if !t.enabled(workspec.KindTicket) {
		return
	}
	for id, refs := range t.refs {
		var added []workspec.Ref
		for _, ref := range refs {
			if ref.Kind != workspec.KindPR || ref.Provenance != workspec.FromCreation {
				continue
			}
			pr, ok := t.prs[ref.Key()]
			if !ok {
				continue
			}
			if ticket, found := workspec.TicketFromBranch(pr.HeadRef); found {
				added = append(added, ticket)
			}
		}
		if len(added) > 0 {
			t.refs[id] = workspec.Dedupe(append(refs, added...))
		}
	}
}

// forget drops everything keyed to a session that is no longer on the board,
// and every resolved artifact no remaining session refers to.
//
// A session that ends stops being visited, so without this its entries stay
// resident for the life of a process that runs for days: measured at about
// 1.3 KB per session that ever existed.
//
// Artifacts are swept by reachability rather than by age because two sessions
// routinely name the same pull request: dropping one session's entry must not
// take the row the other one is still showing.
func (t *Tracker) forget(sessions []Session) {
	live := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		live[session.ID] = true
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for id := range t.refs {
		if !live[id] {
			delete(t.refs, id)
		}
	}

	reachable := make(map[string]bool, len(t.refs))
	for _, refs := range t.refs {
		for _, ref := range refs {
			reachable[ref.Key()] = true
		}
	}
	for key := range t.prs {
		if !reachable[key] {
			delete(t.prs, key)
		}
	}
	for key := range t.tickets {
		if !reachable[key] {
			delete(t.tickets, key)
		}
	}
	for key := range t.fetched {
		if !reachable[key] {
			delete(t.fetched, key)
		}
	}
	// Sightings go with the artifacts. A session leaving the board takes
	// them, and a session that comes back -- the archived scope, say --
	// starts its settled rows on a fresh day, the same standing its pull
	// requests have when they are fetched again.
	//
	// A provider that is off has no references, so none of its sightings are reachable; they
	// are kept for when it is back on, or its settled rows would return to the board for a day.
	for key := range t.seen {
		if !reachable[key] && t.keyEnabled(key) {
			t.forgetSeenLocked(key)
		}
	}
	dirs := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		dirs[session.Dir] = true
	}
	for dir := range t.names {
		if !dirs[dir] {
			delete(t.names, dir)
		}
	}
}

// due is what to ask about this pass: every reference whose cached answer has aged out, most looked
// at first, capped so one pass cannot spend the whole quota.
//
// The order is the point. Before, a pass handed the whole stale set to the sources in whatever order
// the sessions happened to be in, so on a board with two hundred references the one under the
// operator's cursor was refreshed at the same rate as a pull request nobody has thought about since
// Tuesday. Now the cursor's references go in the first pass and the rest drain behind them.
//
// A reference reachable from more than one session takes the strongest tier any of them gives it,
// which is why the tier is resolved for every session before anything is cut: a pull request on
// screen is on screen, whatever else also mentions it.
func (t *Tracker) due(sessions []Session) []workspec.Ref {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := t.now()
	at := map[string]int{}
	var candidates []candidate

	for _, session := range sessions {
		sessionTier := tierIdle
		switch {
		case session.Visible:
			sessionTier = tierVisible
		case session.Live:
			sessionTier = tierLive
		}
		for _, ref := range t.refs[session.ID] {
			key := ref.Key()
			if i, seen := at[key]; seen {
				if sessionTier < candidates[i].tier {
					candidates[i].tier = sessionTier
				}
				continue
			}
			at[key] = len(candidates)
			candidates = append(candidates, candidate{ref: ref, tier: sessionTier})
		}
	}

	// Staleness is judged after the tier is final, because the tier is what sets the interval.
	kept := candidates[:0]
	for _, c := range candidates {
		if fetched, ok := t.fetched[c.ref.Key()]; ok && now.Sub(fetched) < t.interval(c) {
			continue
		}
		kept = append(kept, c)
	}

	// Stable, so within a tier the caller's own order survives: the view hands its sessions
	// over in the order it drew them, and that is a better tie-break than any this can invent.
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].tier < kept[j].tier })

	due := make([]workspec.Ref, 0, len(kept))
	spent := map[workspec.Kind]int{}
	for _, c := range kept {
		if spent[c.ref.Kind] >= RefreshBudget {
			continue
		}
		spent[c.ref.Kind]++
		due = append(due, c.ref)
	}
	return due
}

// tier is how much somebody is looking at a reference, strongest first.
type tier int

const (
	tierVisible tier = iota
	tierLive
	tierIdle
)

// candidate is one reference due consideration this pass, with the strongest claim on it.
type candidate struct {
	ref  workspec.Ref
	tier tier
}

// interval is how long this reference's answer is believed.
//
// Called under the read lock, because a finished pull request earns its long interval from the
// answer already in the cache rather than from anything the session says.
func (t *Tracker) interval(c candidate) time.Duration {
	if pr, ok := t.prs[c.ref.Key()]; ok && (pr.State == forge.PRMerged || pr.State == forge.PRClosed) {
		// Finished, and finished outranks visible: re-reading a merged pull request every
		// twenty seconds because the cursor is sitting on it buys nothing at all.
		return TerminalInterval
	}
	switch c.tier {
	case tierVisible:
		return VisibleInterval
	case tierLive:
		return LiveInterval
	default:
		return IdleInterval
	}
}

// For is what a session is working on, as of the last successful look.
//
// Never blocks and never fetches. A reference with no resolved state simply has no entry; Looked
// is what says whether that means "no such thing" or "not asked yet".
func (t *Tracker) For(sessionID string) Work {
	t.mu.RLock()
	defer t.mu.RUnlock()

	work := Work{
		SessionID: sessionID,
		Refs:      t.refs[sessionID],
		PRs:       map[string]forge.PR{},
		Tickets:   map[string]forge.Ticket{},
		Retired:   map[string]bool{},
		Looked:    map[string]bool{},
	}
	now := t.now()
	for _, ref := range work.Refs {
		key := ref.Key()
		if t.retiredLocked(key, now) {
			work.Retired[key] = true
		}
		if pr, ok := t.prs[key]; ok {
			work.PRs[key] = pr
		}
		if ticket, ok := t.tickets[key]; ok {
			work.Tickets[key] = ticket
		}
		if _, ok := t.fetched[key]; ok {
			work.Looked[key] = true
		}
	}
	return work
}

// Health is whether each source could be read at all; nothing draws it yet. A provider that is
// off reads as off, which is not a failure.
func (t *Tracker) Health() (github, linear forge.Health) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	github, linear = t.health.gh, t.health.linear
	if t.GitHub == nil {
		github = forge.Health{Off: true, Reason: "GitHub is switched off"}
	}
	if t.Linear == nil {
		linear = forge.Health{Off: true, Reason: "Linear is switched off"}
	}
	return github, linear
}
