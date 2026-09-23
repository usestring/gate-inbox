package forge

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/workspec"
)

func pr(number int) workspec.Ref {
	return workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/sample-repo", Number: number}
}

func githubReturning(t *testing.T, response string) (*GitHub, *[]string) {
	t.Helper()
	var queries []string
	return &GitHub{
		Run: func(args ...string) ([]byte, error) {
			queries = append(queries, strings.Join(args, " "))
			return []byte(response), nil
		},
		Now: func() time.Time { return time.Unix(1700000000, 0) },
	}, &queries
}

// One request for the whole board. Twenty sessions must not be twenty round-trips on a ticker.
func TestEveryPullRequestIsAskedForInOneQuery(t *testing.T) {
	gh, queries := githubReturning(t, `{"data":{}}`)

	gh.PRs([]workspec.Ref{pr(1), pr(2), pr(3)})

	if len(*queries) != 1 {
		t.Fatalf("made %d requests, want 1", len(*queries))
	}
	for _, number := range []string{"number: 1", "number: 2", "number: 3"} {
		if !strings.Contains((*queries)[0], number) {
			t.Errorf("query does not ask for %q", number)
		}
	}
}

// Nothing to look up is a healthy no-op, not a failed look. The difference decides whether the
// work view says "ok" or shows a problem the operator cannot act on.
func TestAskingAboutNothingIsHealthy(t *testing.T) {
	gh, queries := githubReturning(t, `{"data":{}}`)

	resolved, health := gh.PRs(nil)
	if !health.OK {
		t.Errorf("health = %+v, want ok", health)
	}
	if len(resolved) != 0 || len(*queries) != 0 {
		t.Errorf("resolved %d and made %d requests", len(resolved), len(*queries))
	}
}

func TestAMalformedRepositoryIsSkippedRatherThanAsked(t *testing.T) {
	gh, queries := githubReturning(t, `{"data":{}}`)

	gh.PRs([]workspec.Ref{{Kind: workspec.KindPR, Repo: "no-slash", Number: 1}})
	if len(*queries) != 0 {
		t.Errorf("asked GitHub about %q", "no-slash")
	}
}

func response(fields string) string {
	return `{"data":{"p0":{"pullRequest":{` + fields + `,"repository":{"nameWithOwner":"example-org/sample-repo"}}}}}`
}

func TestPullRequestStatesAreRead(t *testing.T) {
	for _, test := range []struct {
		fields string
		want   PRState
	}{
		{`"number":1,"state":"OPEN"`, PROpen},
		{`"number":1,"state":"OPEN","isDraft":true`, PRDraft},
		{`"number":1,"state":"MERGED"`, PRMerged},
		{`"number":1,"state":"CLOSED"`, PRClosed},
	} {
		gh, _ := githubReturning(t, response(test.fields))
		resolved, _ := gh.PRs([]workspec.Ref{pr(1)})
		got, ok := resolved[pr(1).Key()]
		if !ok {
			t.Fatalf("%s: not resolved", test.fields)
		}
		if got.State != test.want {
			t.Errorf("%s: state = %q, want %q", test.fields, got.State, test.want)
		}
	}
}

// A row must never say green about something that has not reported. Anything that is not an
// explicit success reads as pending, including states GitHub has not invented yet.
func TestUnreportedChecksAreNeverGreen(t *testing.T) {
	for _, state := range []string{"PENDING", "EXPECTED", "SOMETHING_NEW"} {
		gh, _ := githubReturning(t, response(
			`"number":1,"state":"OPEN","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"`+state+`","contexts":{"nodes":[]}}}}]}`))

		resolved, _ := gh.PRs([]workspec.Ref{pr(1)})
		if got := resolved[pr(1).Key()].Checks; got != ChecksPending {
			t.Errorf("%s: checks = %q, want pending", state, got)
		}
	}
}

func TestNoChecksIsNotTheSameAsChecksNotReported(t *testing.T) {
	gh, _ := githubReturning(t, response(`"number":1,"state":"OPEN"`))

	resolved, _ := gh.PRs([]workspec.Ref{pr(1)})
	if got := resolved[pr(1).Key()].Checks; got != ChecksNone {
		t.Errorf("checks = %q, want none", got)
	}
}

// The count is what lets a row say "2 checks failing" instead of only that something is.
func TestFailingChecksAreCounted(t *testing.T) {
	gh, _ := githubReturning(t, response(
		`"number":1,"state":"OPEN","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE","contexts":{"nodes":[
			{"conclusion":"FAILURE"},{"conclusion":"SUCCESS"},{"conclusion":"TIMED_OUT"},{"state":"ERROR"}]}}}}]}`))

	resolved, _ := gh.PRs([]workspec.Ref{pr(1)})
	got := resolved[pr(1).Key()]
	if got.Checks != ChecksFailing {
		t.Fatalf("checks = %q", got.Checks)
	}
	if got.FailingChecks != 3 {
		t.Errorf("failing = %d, want 3", got.FailingChecks)
	}
}

// GitHub reports UNKNOWN while it is still working the merge out. Treating that as a conflict
// would flash every freshly pushed branch as needing you.
func TestAnUncomputedMergeIsNotAConflict(t *testing.T) {
	for _, mergeable := range []string{"MERGEABLE", "UNKNOWN", ""} {
		gh, _ := githubReturning(t, response(`"number":1,"state":"OPEN","mergeable":"`+mergeable+`"`))
		resolved, _ := gh.PRs([]workspec.Ref{pr(1)})
		if !resolved[pr(1).Key()].Mergeable {
			t.Errorf("mergeable %q read as a conflict", mergeable)
		}
	}

	gh, _ := githubReturning(t, response(`"number":1,"state":"OPEN","mergeable":"CONFLICTING"`))
	resolved, _ := gh.PRs([]workspec.Ref{pr(1)})
	if resolved[pr(1).Key()].Mergeable {
		t.Error("CONFLICTING read as mergeable")
	}
}

// The ordering key for the work view, and the same promise the board makes: whatever wants you is
// at the top.
func TestWhatNeedsYou(t *testing.T) {
	for _, test := range []struct {
		name string
		pr   PR
		want bool
	}{
		{"failing checks", PR{State: PROpen, Checks: ChecksFailing, Mergeable: true}, true},
		{"changes requested", PR{State: PROpen, Review: ReviewChangesRequested, Mergeable: true}, true},
		{"conflicting", PR{State: PROpen, Mergeable: false}, true},
		{"open and green", PR{State: PROpen, Checks: ChecksPassing, Mergeable: true}, false},
		{"merged, whatever else", PR{State: PRMerged, Checks: ChecksFailing, Mergeable: false}, false},
		{"closed", PR{State: PRClosed, Checks: ChecksFailing}, false},
		// A draft is the author saying it is not ready, so it is not waiting on a reviewer.
		{"draft with red checks", PR{State: PRDraft, Checks: ChecksFailing}, false},
	} {
		if got := test.pr.NeedsYou(); got != test.want {
			t.Errorf("%s: NeedsYou = %v, want %v", test.name, got, test.want)
		}
	}
}

// The one failure an operator fixes with a single command, so it has to be named rather than
// surfaced as an exit status.
func TestAnUnauthenticatedGhSaysSo(t *testing.T) {
	gh := &GitHub{Run: func(...string) ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("gh: To get started with GitHub CLI, please run: gh auth login\n")}
	}}

	resolved, health := gh.PRs([]workspec.Ref{pr(1)})
	if health.OK {
		t.Fatal("an unauthenticated gh reported healthy")
	}
	if !strings.Contains(health.Reason, "gh auth login") {
		t.Errorf("reason = %q, want it to name the fix", health.Reason)
	}
	if resolved != nil {
		t.Error("returned results from a failed look")
	}
}

// A failed look returns nothing rather than an empty map, so a caller cannot mistake it for
// "asked, and nothing is there".
func TestAFailedLookIsNotAnEmptyResult(t *testing.T) {
	gh := &GitHub{Run: func(...string) ([]byte, error) { return nil, errors.New("network is unreachable") }}

	resolved, health := gh.PRs([]workspec.Ref{pr(1)})
	if health.OK || resolved != nil {
		t.Errorf("resolved = %+v, health = %+v", resolved, health)
	}
}

func TestGarbageFromGhIsAFailedLook(t *testing.T) {
	gh, _ := githubReturning(t, "this is not json")

	if _, health := gh.PRs([]workspec.Ref{pr(1)}); health.OK {
		t.Error("unparseable output reported healthy")
	}
}

/* --------------------------------------------------------------------- staying under the quota */

var testNow = time.Unix(1700000000, 0)

// githubAnswering is a gh that writes a body and then exits with the status gh really uses: a reply
// carrying GraphQL errors goes to stdout in full and comes back as exit 1.
func githubAnswering(body, stderr string) *GitHub {
	return &GitHub{
		Run: func(...string) ([]byte, error) {
			if stderr == "" {
				return []byte(body), nil
			}
			return []byte(body), &exec.ExitError{Stderr: []byte(stderr)}
		},
		Now: func() time.Time { return testNow },
	}
}

// The failure that made this expensive. gh exits non-zero for the whole batch when any one
// reference names something that is not there, and reading that as a failed look threw away
// everything else in the same response — so nothing was cached, everything came due again on the
// next tick, and the board re-asked the entire board every thirty seconds over a number somebody
// typed in prose once.
func TestOneReferenceThatNamesNothingDoesNotFailTheBatch(t *testing.T) {
	body := `{"data":{
		"p0":{"pullRequest":{"number":1,"state":"OPEN","repository":{"nameWithOwner":"example-org/sample-repo"}}},
		"p1":null},
		"errors":[{"type":"NOT_FOUND","path":["p1"],"message":"Could not resolve to a Repository with the name 'example-org/nope'."}]}`
	gh := githubAnswering(body, "gh: Could not resolve to a Repository with the name 'example-org/nope'.\n")

	missing := workspec.Ref{Kind: workspec.KindPR, Repo: "example-org/nope", Number: 9}
	resolved, health := gh.PRs([]workspec.Ref{pr(1), missing})

	if !health.OK {
		t.Fatalf("health = %+v, want ok — one absent reference is not a failed look", health)
	}
	if _, ok := resolved[pr(1).Key()]; !ok {
		t.Error("the pull request that answered was thrown away with the one that did not")
	}
	// Both are settled: one exists, one does not, and neither is worth asking about again
	// before its interval is up.
	for _, ref := range []workspec.Ref{pr(1), missing} {
		if !health.Answered[ref.Key()] {
			t.Errorf("%s not marked answered, so it comes due again on the next tick", ref.Key())
		}
	}
}

// The primary quota arrives inside a perfectly valid 200. Its reset is the only honest answer to
// "when should I ask again", and asking sooner spends a budget that is already gone.
func TestAPrimaryRateLimitWaitsForTheReset(t *testing.T) {
	reset := testNow.Add(37 * time.Minute)
	body := `{"data":{"rateLimit":{"remaining":0,"resetAt":"` + reset.UTC().Format(time.RFC3339) + `"}},
		"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`
	gh := githubAnswering(body, "gh: API rate limit exceeded\n")

	_, health := gh.PRs([]workspec.Ref{pr(1)})
	if health.OK {
		t.Fatal("a rate-limited look reported healthy")
	}
	if !health.RetryAfter.Equal(reset) {
		t.Errorf("RetryAfter = %v, want the reset at %v", health.RetryAfter, reset)
	}
}

// The secondary limit names no reset and clears on its own in about a minute, so waiting for the
// hourly reset would blank the work column for an hour over a momentary burst.
func TestASecondaryRateLimitBacksOffBriefly(t *testing.T) {
	gh := githubAnswering("", "gh: You have exceeded a secondary rate limit\n")

	_, health := gh.PRs([]workspec.Ref{pr(1)})
	if health.OK {
		t.Fatal("a rate-limited look reported healthy")
	}
	if want := testNow.Add(secondaryLimitBackoff); !health.RetryAfter.Equal(want) {
		t.Errorf("RetryAfter = %v, want %v", health.RetryAfter, want)
	}
}

// The inbox is a background process on a machine where gh, the review skills and the operator all
// draw on the same 5000 points an hour. It is not entitled to the last of them.
func TestTheLastOfTheQuotaIsLeftAlone(t *testing.T) {
	reset := testNow.Add(20 * time.Minute)
	body := `{"data":{"rateLimit":{"remaining":` + strconv.Itoa(budgetReserve-1) +
		`,"resetAt":"` + reset.UTC().Format(time.RFC3339) + `"},
		"p0":{"pullRequest":{"number":1,"state":"OPEN","repository":{"nameWithOwner":"example-org/sample-repo"}}}}}`
	gh := githubAnswering(body, "")

	resolved, health := gh.PRs([]workspec.Ref{pr(1)})
	if !health.OK {
		t.Fatalf("health = %+v — a look that succeeded is healthy however little is left", health)
	}
	if _, ok := resolved[pr(1).Key()]; !ok {
		t.Error("the answer was discarded along with the budget")
	}
	if !health.RetryAfter.Equal(reset) {
		t.Errorf("RetryAfter = %v, want to stand down until %v", health.RetryAfter, reset)
	}
}

// A healthy look settles everything it was asked about, including the references it found nothing
// for. That is what keeps a "#4213" somebody wrote in prose from being re-asked forever.
func TestAHealthyLookSettlesEverythingItWasAskedAbout(t *testing.T) {
	// GitHub answers for every alias it was given, so the reference that resolved to nothing
	// is present and null rather than missing.
	body := `{"data":{
		"p0":{"pullRequest":{"number":1,"state":"OPEN","repository":{"nameWithOwner":"example-org/sample-repo"}}},
		"p1":{"pullRequest":null}}}`
	gh := githubAnswering(body, "")

	_, health := gh.PRs([]workspec.Ref{pr(1), pr(2)})
	if !health.OK {
		t.Fatalf("health = %+v", health)
	}
	for _, ref := range []workspec.Ref{pr(1), pr(2)} {
		if !health.Answered[ref.Key()] {
			t.Errorf("%s not marked answered", ref.Key())
		}
	}
}

// An unauthenticated gh will not authenticate itself on a ticker. Asking twice a minute until a
// person runs a command buys nothing.
func TestAnUnauthenticatedGhIsLeftAloneForAWhile(t *testing.T) {
	gh := githubAnswering("", "gh: To get started with GitHub CLI, please run: gh auth login\n")

	_, health := gh.PRs([]workspec.Ref{pr(1)})
	if want := testNow.Add(credentialBackoff); !health.RetryAfter.Equal(want) {
		t.Errorf("RetryAfter = %v, want %v", health.RetryAfter, want)
	}
}
