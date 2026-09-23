// Package forge asks GitHub and Linear what the things a session is working on are doing now.
//
// The inbox's thesis is that a decision needing a person should not be buried in a pane nobody is
// looking at. A review with changes requested and a red CI run are that same thing arriving from
// outside the terminal, and they are the one class of "this session needs you" v1 is blind to.
//
// Two rules shape everything here, and both are about not being wrong rather than about being
// fast. Nothing on a render path may block on a network call, so every read is a background
// refresh into a cache and the board draws whatever was last resolved. And a failure is a state
// rather than an error: "we could not look" is not "nothing is wrong", the same distinction the
// tmux badge draws when it cannot reach the server.
package forge

import (
	"time"

	"github.com/usestring/gate-inbox/internal/workspec"
)

// PRState is where a pull request stands.
type PRState string

const (
	PROpen   PRState = "open"
	PRDraft  PRState = "draft"
	PRMerged PRState = "merged"
	PRClosed PRState = "closed"
)

// ChecksState is what CI says.
type ChecksState string

const (
	ChecksPassing ChecksState = "passing"
	ChecksFailing ChecksState = "failing"
	ChecksPending ChecksState = "pending"
	// ChecksNone is a pull request with no checks configured, which is not the same as one
	// whose checks have not reported yet.
	ChecksNone ChecksState = "none"
)

// ReviewState is what the humans say.
type ReviewState string

const (
	ReviewApproved         ReviewState = "approved"
	ReviewChangesRequested ReviewState = "changes-requested"
	ReviewPending          ReviewState = "pending"
)

// PR is a pull request as of the last successful look.
type PR struct {
	Repo      string
	Number    int
	Title     string
	State     PRState
	Checks    ChecksState
	Review    ReviewState
	URL       string
	Mergeable bool
	// HeadRef is the branch the pull request merges from. When it is Linear-shaped it names the
	// ticket the work is for, which is how a session that opened a PR from a worktree the board
	// cannot see still gets its ticket.
	HeadRef string
	// FailingChecks is how many are red, for a row that says "2 checks failing" rather than
	// just that something is.
	FailingChecks int
	FetchedAt     time.Time
}

// NeedsYou reports whether this pull request is waiting on a person.
//
// This is the ordering key for the work view, and it is deliberately the same promise the board
// makes: whatever wants you is at the top. Merged and closed want nothing; a draft is the author
// saying it is not ready, so it does not want a reviewer either.
func (p PR) NeedsYou() bool {
	if p.State == PRMerged || p.State == PRClosed || p.State == PRDraft {
		return false
	}
	return p.Checks == ChecksFailing || p.Review == ReviewChangesRequested || !p.Mergeable
}

// Done reports whether the pull request is over: merged, or closed without
// merging. Nothing about a done pull request can want a person again.
func (p PR) Done() bool { return p.State == PRMerged || p.State == PRClosed }

// Ticket is a Linear issue as of the last successful look.
type Ticket struct {
	Identifier string
	Title      string
	State      string
	// StateType is Linear's own category — backlog, unstarted, started, completed, canceled —
	// which is what a rule should test, because the state *name* is per-team and renameable.
	StateType string
	Assignee  string
	URL       string
	FetchedAt time.Time
}

// Done reports whether the ticket is closed out, by Linear's category rather than by its name.
func (t Ticket) Done() bool { return t.StateType == "completed" || t.StateType == "canceled" }

// Health is whether a source could be read at all, so a reader can be told "we could not look"
// instead of drawing every row as though nothing were happening.
type Health struct {
	OK bool
	// Off is a source the operator has not turned on: disabled in the config, or Linear with no
	// LINEAR_API_KEY. Nothing is asked of it, so it can neither succeed nor fail, and Reason says
	// why it is off rather than what went wrong.
	Off bool
	// Reason is why not, in the operator's terms — an unauthenticated gh, a 401 from Linear.
	Reason string
	// RetryAfter is the earliest this source is worth asking again, zero when the caller should
	// simply follow its own intervals.
	//
	// A source that has just said "you are over your quota" answers a re-ask by spending more
	// of a quota that is already gone, and a refresh ticker will happily do that every thirty
	// seconds until the hour turns over. A rate limit and a rejected credential are both of
	// that shape: the answer cannot change until something outside this process changes, so the
	// source names when it is worth coming back and the caller waits.
	RetryAfter time.Time
	// Answered is every reference key this look settled, whether or not it found anything.
	//
	// Resolving to nothing is an answer, and a common one: workspec's ticket pattern matches
	// anything shaped like ABC-123, and its bare pull request pattern matches any number
	// somebody wrote after the word, so a board always carries references naming nothing. A
	// caller that remembers only what it *found* leaves those unresolved forever and asks again
	// on every tick — an unbounded loop aimed at references that can never resolve, which is
	// what actually exhausts a quota.
	//
	// It is also what makes a partial look worth keeping. One reference naming a repository
	// that does not exist makes gh exit non-zero for the whole batch, and the thirty pull
	// requests that answered perfectly well in that same response are not a failed look. This
	// says which ones those were, independently of OK.
	Answered map[string]bool
}

// credentialBackoff is how long a source that has rejected us is left alone.
//
// An unauthenticated gh and a rejected Linear key are the same situation, which is why both sources
// share this: the answer is a fixed no until a person runs a command, and asking twice a minute in
// the meantime buys nothing.
const credentialBackoff = 10 * time.Minute

// Failed reports whether this source has actually been read and could not be.
//
// The zero Health is not a failure, it is a source nobody has looked at yet — which is the state
// every source starts in, and the state one stays in while nothing of its kind is due. Reading a
// bare !OK as a problem puts "github unavailable:" with no reason on the board before the first
// look has even happened.
func (h Health) Failed() bool { return !h.OK && !h.Off && h.Reason != "" }

// answeredKeys is every reference of one kind that was asked about, for a look that settled all of
// them: the ordinary healthy case, named once rather than rebuilt at each return.
func answeredKeys(refs []workspec.Ref, kind workspec.Kind) map[string]bool {
	answered := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if ref.Kind == kind {
			answered[ref.Key()] = true
		}
	}
	return answered
}

// PRResolver and TicketResolver look up live state for references a session is working on.
//
// Two interfaces rather than one, because no source answers for both: GitHub knows nothing about
// tickets and Linear nothing about pull requests. A combined interface would mean each
// implementation carrying a stub for the half it cannot answer, and a stub that returns "healthy,
// nothing found" is indistinguishable from a real empty answer.
//
// Both implementations batch — one query for every pull request, one per team for the tickets.
// A board with twenty sessions would otherwise be forty round-trips per refresh, on a ticker.
type PRResolver interface {
	PRs(refs []workspec.Ref) (map[string]PR, Health)
}

type TicketResolver interface {
	Tickets(refs []workspec.Ref) (map[string]Ticket, Health)
}
