package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/workspec"
)

// lookupTimeout bounds one gh invocation.
//
// gh talks to the network and this runs on the refresh goroutine, so a hung one holds that
// goroutine for as long as it hangs: the work column stops updating with no health state to say
// why, which is indistinguishable from a board with nothing to report.
const lookupTimeout = 45 * time.Second

// budgetReserve is how much of the hourly GraphQL allowance the inbox refuses to spend.
//
// GitHub's GraphQL quota is 5000 points an hour and the inbox is a background process on a
// developer's machine: it is emphatically not entitled to the last of a budget that gh, the review
// skills, and the operator's own commands all draw on. When a look leaves less than this, the next
// one waits for the quota to reset rather than racing everything else to the floor.
const budgetReserve = 1000

// secondaryLimitBackoff is how long to stand down after GitHub's secondary rate limit.
//
// Unlike the primary quota the secondary limit names no reset, and it is a burst limit: it clears
// on its own in about a minute. Waiting for the hourly reset would blank the work column for an
// hour over a momentary burst.
const secondaryLimitBackoff = 2 * time.Minute

// GitHub reads pull request state through the gh CLI.
//
// Through gh rather than a token of our own: whatever authentication the operator already has is
// the authentication this uses, so the inbox stays a plain user-launched process with no secret to
// manage — the same reason it has no launchd job. The cost is that gh must be installed and logged
// in, which is a Health state rather than a crash.
type GitHub struct {
	// Run executes gh, injected so tests can describe a response without a network.
	Run func(args ...string) ([]byte, error)
	Now func() time.Time
}

func NewGitHub() *GitHub {
	return &GitHub{
		Run: func(args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "gh", args...)
			return cmd.Output()
		},
		Now: time.Now,
	}
}

func (g *GitHub) now() time.Time {
	if g.Now == nil {
		return time.Now()
	}
	return g.Now()
}

// prQuery builds one GraphQL document asking for every pull request at once.
//
// Aliased fields rather than a loop of requests: a board with twenty sessions would otherwise be
// twenty round-trips per refresh, on a ticker, forever.
//
// The aliases it returns map each one back to the reference that asked, which is what lets a
// per-alias error be attributed to the one reference it is about instead of failing the batch.
//
// rateLimit rides along in the same document. It is free — GitHub scores it at zero — and it is the
// only way to find out what is left before spending it.
func prQuery(refs []workspec.Ref) (string, map[string]workspec.Ref, bool) {
	var parts []string
	aliases := map[string]workspec.Ref{}
	for i, ref := range refs {
		if ref.Kind != workspec.KindPR {
			continue
		}
		owner, name, found := strings.Cut(ref.Repo, "/")
		if !found || owner == "" || name == "" {
			continue
		}
		alias := fmt.Sprintf("p%d", i)
		aliases[alias] = ref
		parts = append(parts, fmt.Sprintf(
			`%s: repository(owner: %q, name: %q) { pullRequest(number: %d) { %s } }`,
			alias, owner, name, ref.Number, prFields))
	}
	if len(parts) == 0 {
		return "", nil, false
	}
	return "query { rateLimit { remaining resetAt } " + strings.Join(parts, " ") + "}", aliases, true
}

const prFields = `
	number title url isDraft state mergeable headRefName
	reviewDecision
	commits(last: 1) { nodes { commit { statusCheckRollup { state contexts(last: 100) {
		nodes { ... on CheckRun { conclusion } ... on StatusContext { state } }
	} } } } }
	repository { nameWithOwner }
`

type prNode struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	IsDraft        bool   `json:"isDraft"`
	State          string `json:"state"`
	Mergeable      string `json:"mergeable"`
	ReviewDecision string `json:"reviewDecision"`
	HeadRefName    string `json:"headRefName"`
	Repository     struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						Nodes []struct {
							Conclusion string `json:"conclusion"`
							State      string `json:"state"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// prResponse is a GraphQL reply, including the parts of it that are errors.
//
// Data stays raw because it is not one shape: rateLimit sits in the same object as the aliased
// repositories, and an alias GitHub could not resolve is null there. Decoding each entry on demand
// lets every one of those be absent or wrong without costing the reply the rest of its answers.
type prResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Path    []any  `json:"path"`
	} `json:"errors"`
}

// budget is what GitHub said was left, or nil if it did not say.
func (r prResponse) budget() *rateBudget {
	var limit rateBudget
	if json.Unmarshal(r.Data["rateLimit"], &limit) != nil {
		return nil
	}
	return &limit
}

// pullRequest is the pull request one alias resolved to. The second return says whether GitHub
// answered for that alias at all, which is a different question from whether it found anything.
func (r prResponse) pullRequest(alias string) (*prNode, bool) {
	raw, present := r.Data[alias]
	if !present {
		return nil, false
	}
	// A repository GitHub could not resolve comes back as a null alias, which decodes to the
	// zero entry rather than failing: answered for, with nothing there.
	var entry struct {
		PullRequest *prNode `json:"pullRequest"`
	}
	if json.Unmarshal(raw, &entry) != nil {
		return nil, true
	}
	return entry.PullRequest, true
}

// PRs resolves every pull request reference in one request.
//
// The shape of it comes from one fact about gh: a GraphQL reply carrying errors is written to
// stdout in full and *then* exits non-zero. Since workspec scans prose, a reference naming a
// repository or a number that does not exist is ordinary, so reading that exit status as a failed
// look would throw away a whole batch of good answers over one of them — and, because nothing would
// then be settled, ask for them all again on the next tick.
//
// So the body is parsed whether or not gh was happy with it, per-alias errors are attributed to the
// one reference they name, and only a failure that really is about the whole look — no
// authentication, no network, a quota — comes back as unhealthy.
func (g *GitHub) PRs(refs []workspec.Ref) (map[string]PR, Health) {
	query, aliases, ok := prQuery(refs)
	if !ok {
		// Nothing to ask about is a healthy no-op, not a failure to look.
		return map[string]PR{}, Health{OK: true, Answered: map[string]bool{}}
	}

	out, runErr := g.Run("api", "graphql", "-f", "query="+query)

	var response prResponse
	if json.Unmarshal(out, &response) != nil {
		// Nothing usable came back at all, so this is the one case with no partial
		// answer to keep: an unauthenticated gh, a missing binary, a dead network.
		if runErr != nil {
			return nil, g.failed(ghReason(runErr), rateLimitedBy(runErr), nil)
		}
		return nil, Health{Reason: "could not read gh's response"}
	}

	resolved := map[string]PR{}
	answered := map[string]bool{}
	for alias, ref := range aliases {
		node, asked := response.pullRequest(alias)
		if !asked {
			continue
		}
		answered[ref.Key()] = true
		if node == nil {
			continue
		}
		pr := prFrom(*node, g.now())
		resolved[workspec.Ref{Kind: workspec.KindPR, Repo: pr.Repo, Number: pr.Number}.Key()] = pr
	}

	// A NOT_FOUND names its alias in the error path, which is the source telling us that one
	// reference is settled: there is no such repository or pull request and there never will
	// be. Recording it is what stops the re-ask. Anything else is about the look as a whole.
	limited, wider := false, ""
	for _, failure := range response.Errors {
		ref, known := aliases[errorAlias(failure.Path)]
		switch {
		case strings.EqualFold(failure.Type, "RATE_LIMITED"):
			limited = true
		case known && strings.EqualFold(failure.Type, "NOT_FOUND"):
			answered[ref.Key()] = true
		case wider == "":
			wider = firstLine(failure.Message)
		}
	}
	limited = limited || rateLimitedBy(runErr)

	health := Health{OK: true}
	switch {
	case limited:
		health = g.failed("GitHub rate limit reached", true, response.budget())
	case wider != "":
		// Something went wrong that is not about one reference. The answers that did come
		// back still stand; the header says the look was incomplete.
		health = Health{Reason: wider}
	case runErr != nil && len(response.Errors) == 0:
		health = g.failed(ghReason(runErr), false, response.budget())
	default:
		// Healthy, and possibly the last one for a while. Spending the reserve is what
		// turns an ordinary busy hour into a board that cannot look at anything,
		// including the pull request the operator is actually waiting on.
		if budget := response.budget(); budget != nil && budget.Remaining < budgetReserve {
			health.RetryAfter = budget.ResetAt
		}
	}
	health.Answered = answered
	return resolved, health
}

// errorAlias is the alias a GraphQL error is about — the head of its path, which for both
// `["p1"]` and `["p0","pullRequest"]` is the alias that asked.
func errorAlias(path []any) string {
	if len(path) == 0 {
		return ""
	}
	alias, _ := path[0].(string)
	return alias
}

// rateLimitedBy reports whether gh itself refused over a quota.
//
// The two GitHub limits arrive differently: the primary quota is a RATE_LIMITED error inside a
// perfectly valid 200, which PRs reads out of the body, while the secondary one is a 403 with
// nothing in the body at all — only what gh printed.
func rateLimitedBy(runErr error) bool {
	if runErr == nil {
		return false
	}
	text := strings.ToLower(runErr.Error() + " " + stderrOf(runErr))
	return strings.Contains(text, "rate limit") || strings.Contains(text, "abuse detection")
}

// failed builds an unhealthy Health, working out when it is worth coming back.
//
// A quota that names its reset says so; a secondary limit does not and clears on its own in about a
// minute; a rejected or absent credential will not fix itself on a ticker, so there is no point
// asking again until the operator has had time to do something about it.
func (g *GitHub) failed(reason string, limited bool, budget *rateBudget) Health {
	health := Health{Reason: reason}
	switch {
	case limited && budget != nil && !budget.ResetAt.IsZero():
		health.RetryAfter = budget.ResetAt
	case limited:
		health.RetryAfter = g.now().Add(secondaryLimitBackoff)
	case strings.Contains(reason, "gh auth login"), strings.Contains(reason, "not installed"):
		health.RetryAfter = g.now().Add(credentialBackoff)
	}
	return health
}

func prFrom(node prNode, at time.Time) PR {
	pr := PR{
		Repo:      node.Repository.NameWithOwner,
		Number:    node.Number,
		Title:     node.Title,
		URL:       node.URL,
		HeadRef:   node.HeadRefName,
		FetchedAt: at,
		// MERGEABLE / CONFLICTING / UNKNOWN. UNKNOWN means GitHub is still computing it, and
		// treating that as a conflict would flash every freshly pushed branch as needing you.
		Mergeable: node.Mergeable != "CONFLICTING",
	}

	switch {
	case strings.EqualFold(node.State, "MERGED"):
		pr.State = PRMerged
	case strings.EqualFold(node.State, "CLOSED"):
		pr.State = PRClosed
	case node.IsDraft:
		pr.State = PRDraft
	default:
		pr.State = PROpen
	}

	switch node.ReviewDecision {
	case "APPROVED":
		pr.Review = ReviewApproved
	case "CHANGES_REQUESTED":
		pr.Review = ReviewChangesRequested
	default:
		pr.Review = ReviewPending
	}

	pr.Checks, pr.FailingChecks = checksFrom(node)
	return pr
}

// checksFrom counts the red ones as well as naming the overall state, so a row can say "2 checks
// failing" rather than only that something is.
func checksFrom(node prNode) (ChecksState, int) {
	if len(node.Commits.Nodes) == 0 || node.Commits.Nodes[0].Commit.StatusCheckRollup == nil {
		return ChecksNone, 0
	}
	rollup := node.Commits.Nodes[0].Commit.StatusCheckRollup

	failing := 0
	for _, context := range rollup.Contexts.Nodes {
		verdict := context.Conclusion
		if verdict == "" {
			verdict = context.State
		}
		switch strings.ToUpper(verdict) {
		case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ERROR":
			failing++
		}
	}

	switch strings.ToUpper(rollup.State) {
	case "SUCCESS":
		return ChecksPassing, 0
	case "FAILURE", "ERROR":
		return ChecksFailing, failing
	default:
		// PENDING, EXPECTED, and anything a future GitHub adds. Pending rather than passing:
		// a row must never say green about something that has not reported.
		return ChecksPending, failing
	}
}

// ghReason turns a gh failure into something an operator can act on.
//
// The distinction that matters is authentication, because that is the one an operator fixes with a
// single command, and the one most likely to be true on a fresh box.
func ghReason(err error) string {
	stderr := stderrOf(err)
	switch {
	case strings.Contains(stderr, "gh auth login"), strings.Contains(stderr, "not logged"):
		return "gh is not authenticated — run gh auth login"
	case strings.Contains(err.Error(), "executable file not found"):
		return "gh is not installed"
	case stderr != "":
		return strings.TrimSpace(firstLine(stderr))
	default:
		return err.Error()
	}
}

// rateBudget is what GitHub says is left of the hourly GraphQL allowance, and when it comes back.
type rateBudget struct {
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

// stderrOf is what gh printed, which is where it puts everything worth reading about a failure.
func stderrOf(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(exit.Stderr)
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
