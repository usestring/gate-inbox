package forge

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/workspec"
)

const feedbackFields = `"number":1,"state":"OPEN","headRefOid":"head","baseRefOid":"base","mergeable":"CONFLICTING",
"reviews":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"review","state":"CHANGES_REQUESTED","submittedAt":"2026-11-09T12:00:00Z","author":{"__typename":"User","login":"reviewer"},"commit":{"oid":"head"}}]},
"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"thread","isResolved":false,"isOutdated":false,"comments":{"nodes":[{"author":{"__typename":"User"},"originalCommit":{"oid":"head"}}]}}]}`

func TestPRFeedbackIsOptInBatchedAndKeepsEvidenceCompleteness(t *testing.T) {
	for _, scenario := range []string{"off", "complete", "truncated-reviews", "truncated-threads", "missing-reviews", "missing-threads", "missing-review-page", "missing-thread-page", "null-reviews", "null-threads", "missing-root", "missing-head"} {
		t.Run(scenario, func(t *testing.T) {
			fields := feedbackFields
			switch scenario {
			case "truncated-reviews":
				fields = strings.Replace(fields, `"reviews":{"pageInfo":{"hasNextPage":false}`, `"reviews":{"pageInfo":{"hasNextPage":true}`, 1)
			case "truncated-threads":
				fields = strings.Replace(fields, `"reviewThreads":{"pageInfo":{"hasNextPage":false}`, `"reviewThreads":{"pageInfo":{"hasNextPage":true}`, 1)
			case "missing-reviews":
				fields = strings.Replace(fields, `"reviews":`, `"unavailableReviews":`, 1)
			case "missing-threads":
				fields = strings.Replace(fields, `"reviewThreads":`, `"unavailableThreads":`, 1)
			case "missing-review-page":
				fields = strings.Replace(fields, `"reviews":{"pageInfo":`, `"reviews":{"unavailablePage":`, 1)
			case "missing-thread-page":
				fields = strings.Replace(fields, `"reviewThreads":{"pageInfo":`, `"reviewThreads":{"unavailablePage":`, 1)
			case "null-reviews":
				fields = strings.Replace(fields, `"nodes":[{"id":"review"`, `"unavailableNodes":[{"id":"review"`, 1)
			case "null-threads":
				fields = strings.Replace(fields, `"nodes":[{"id":"thread"`, `"unavailableNodes":[{"id":"thread"`, 1)
			case "missing-root":
				fields = strings.Replace(fields, `"originalCommit":{"oid":"head"}`, `"originalCommit":null`, 1)
			case "missing-head":
				fields = strings.Replace(fields, `"headRefOid":"head"`, `"headRefOid":""`, 1)
			}
			gh, queries := githubReturning(t, response(fields))
			gh.PRFeedback = scenario != "off"
			prs, health := gh.PRs([]workspec.Ref{pr(1), pr(2)})
			if !health.OK || len(*queries) != 1 {
				t.Fatalf("batch failed: %+v %v", health, *queries)
			}
			asked := strings.Contains((*queries)[0], "reviewThreads(first:")
			if asked != gh.PRFeedback {
				t.Fatalf("opt-in query: %s", (*queries)[0])
			}
			f := prs[pr(1).Key()].Feedback
			if scenario == "off" || scenario == "missing-head" {
				if f != nil {
					t.Fatalf("unsafe snapshot: %+v", f)
				}
				return
			}
			if f == nil || f.HeadSHA != "head" || f.BaseSHA != "base" || f.Mergeability != "CONFLICTING" {
				t.Fatalf("lost evidence: %+v", f)
			}
			wantReviews := scenario != "truncated-reviews" && scenario != "missing-reviews" && scenario != "missing-review-page" && scenario != "null-reviews"
			wantThreads := scenario != "truncated-threads" && scenario != "missing-threads" && scenario != "missing-thread-page" && scenario != "null-threads" && scenario != "missing-root"
			if f.ReviewsComplete != wantReviews || f.ThreadsComplete != wantThreads {
				t.Fatalf("completeness = %+v", f)
			}
			if wantReviews && (f.Reviews[0].HeadSHA != "head" || f.Reviews[0].AuthorType != "User") {
				t.Fatalf("review = %+v", f.Reviews)
			}
			if wantThreads && (f.Threads[0].HeadSHA != "head" || f.Threads[0].AuthorType != "User") {
				t.Fatalf("threads = %+v", f.Threads)
			}
		})
	}
}
