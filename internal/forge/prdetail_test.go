package forge

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const prDetailResponse = `{"data":{
	"rateLimit":{"remaining":4000,"resetAt":"2026-10-23T12:00:00Z"},
	"repository":{"pullRequest":{"number":7,"title":"Fix it","url":"u","body":"Why","isDraft":false,
		"headRefOid":"abc","baseRefName":"main","headRefName":"fix","additions":3,"deletions":1,
		"author":{"login":"ann"},"commitCount":{"totalCount":2},
		"comments":{"nodes":[{"body":"later","createdAt":"2026-10-22T12:00:00Z","author":{"login":"bob"}}]},
		"reviews":{"nodes":[
			{"body":"","state":"COMMENTED","submittedAt":"2026-10-22T10:00:00Z","author":{"login":"bob"}},
			{"body":"","state":"APPROVED","submittedAt":"2026-10-22T11:00:00Z","author":null}]},
		"reviewThreads":{"nodes":[{"isResolved":false,"isOutdated":false,"path":"a.go","line":4,"diffSide":"RIGHT",
			"comments":{"nodes":[{"body":"nit","createdAt":"2026-10-22T09:00:00Z","author":{"login":"bob"}}]}}]},
		"head":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE","contexts":{"nodes":[
			{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"t"},
			{"__typename":"CheckRun","name":"lint","status":"IN_PROGRESS","conclusion":"","detailsUrl":"l"},
			{"__typename":"StatusContext","context":"ci/legacy","state":"SUCCESS","targetUrl":"s"}]}}}}]}
	}}
}}`

// gh --paginate writes each page's array straight after the last.
const prFilesResponse = `[{"filename":"a.go","status":"modified","additions":2,"deletions":1,"patch":"@@ -1 +1,2 @@"}]
[{"filename":"b.go","previous_filename":"old.go","status":"renamed","additions":1,"deletions":0}]`

func githubServing(graphql, files string, filesErr error) *GitHub {
	return &GitHub{
		Run: func(args ...string) ([]byte, error) {
			if args[1] == "graphql" {
				return []byte(graphql), nil
			}
			return []byte(files), filesErr
		},
		Now: func() time.Time { return time.Unix(1700000000, 0) },
	}
}

func TestPullRequestDetailReadsThePage(t *testing.T) {
	detail, health := githubServing(prDetailResponse, prFilesResponse, nil).PullRequestDetail("o/r", 7)
	if !health.OK {
		t.Fatalf("health = %+v", health)
	}
	if detail.Author != "ann" || detail.Commits != 2 || detail.HeadSHA != "abc" || detail.Checks != ChecksFailing {
		t.Errorf("header = %+v", detail)
	}
	// The verdict-less COMMENTED review says nothing in the conversation; the approval does,
	// and a deleted account reads as ghost, as GitHub shows it.
	var timeline []string
	for _, c := range detail.Timeline {
		timeline = append(timeline, c.Author+":"+c.Review)
	}
	if got := strings.Join(timeline, " "); got != "ghost:APPROVED bob:" {
		t.Errorf("timeline = %q", got)
	}
	if len(detail.Threads) != 1 || detail.Threads[0].Line != 4 || detail.Threads[0].Side != "RIGHT" {
		t.Errorf("threads = %+v", detail.Threads)
	}
	var checks []string
	for _, c := range detail.CheckRuns {
		checks = append(checks, c.Name+"="+string(c.State))
	}
	if got := strings.Join(checks, " "); got != "test=failing lint=pending ci/legacy=passing" {
		t.Errorf("checks = %q", got)
	}
	if len(detail.Files) != 2 || detail.Files[1].Previous != "old.go" {
		t.Errorf("files = %+v", detail.Files)
	}
}

// A page with the conversation but no diff would read as a pull request that changes nothing.
func TestPullRequestDetailFailsWithoutItsFiles(t *testing.T) {
	_, health := githubServing(prDetailResponse, "", errors.New("exit status 1")).PullRequestDetail("o/r", 7)
	if health.OK {
		t.Fatal("a failed files listing was reported healthy")
	}
}

func TestPullRequestDetailSaysWhyItFailed(t *testing.T) {
	for name, tc := range map[string]struct{ response, want string }{
		"rate limited": {`{"data":{"rateLimit":{"remaining":0,"resetAt":"2026-10-23T12:00:00Z"}},"errors":[{"type":"RATE_LIMITED","message":"slow down"}]}`, "rate limit"},
		"not found":    {`{"data":{"repository":{"pullRequest":null}},"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a PullRequest"}]}`, "Could not resolve"},
		"unreadable":   {`<html>`, "could not read"},
	} {
		t.Run(name, func(t *testing.T) {
			_, health := githubServing(tc.response, "[]", nil).PullRequestDetail("o/r", 7)
			if health.OK || !strings.Contains(health.Reason, tc.want) {
				t.Errorf("health = %+v, want a reason naming %q", health, tc.want)
			}
		})
	}
}
