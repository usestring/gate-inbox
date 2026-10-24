package forge

import (
	"fmt"
	"strings"
	"testing"
)

const reviewSearchResponse = `{"data":{
	"rateLimit":{"remaining":4000,"resetAt":"2026-10-23T12:00:00Z"},
	"requested":{"nodes":[{"id":"PR_1","number":7,"title":"Fix it","url":"u","headRefOid":"abc",
		"author":{"login":"ann"},"repository":{"nameWithOwner":"o/r"},
		"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]},
		"timelineItems":{"nodes":[
			{"__typename":"ReviewRequestedEvent","id":"RR_other","createdAt":"2026-10-22T10:00:00Z","requestedReviewer":{"__typename":"User","login":"bob"}},
			{"__typename":"ReviewRequestedEvent","id":"RR_me","createdAt":"2026-10-22T11:00:00Z","requestedReviewer":{"__typename":"User","login":"me"}}]},
		"comments":{"nodes":[]}}]},
	"label":{"nodes":[{"id":"PR_2","number":8,"repository":{"nameWithOwner":"o/r"},
		"timelineItems":{"nodes":[{"__typename":"LabeledEvent","id":"LE_1","createdAt":"2026-10-22T09:00:00Z","label":{"name":"gate-review"}}]},
		"comments":{"nodes":[]}}]},
	"comment":{"nodes":[{"id":"PR_3","number":9,"repository":{"nameWithOwner":"o/r"},
		"timelineItems":{"nodes":[]},
		"comments":{"nodes":[
			{"id":"C_quote","body":"we could use /gate review here","author":{"login":"ann"}},
			{"id":"C_ask","body":"thanks\n/gate review please","author":{"login":"ann"}},
			{"id":"C_stranger","body":"/gate review","author":{"login":"eve"}}]}}]}
}}`

// Each trigger keys on the ask that made it, and a comment counts only when a line starts with
// the command and its author may ask.
func TestReviewRequestsKeyOnTheAsk(t *testing.T) {
	gh, queries := githubReturning(t, reviewSearchResponse)
	gh.login = "me"
	search := ReviewSearch{
		Triggers:   []Trigger{TriggerRequested, TriggerLabel, TriggerComment},
		Label:      "gate-review",
		Command:    "/gate review",
		Orgs:       []string{"o"},
		Commenters: []string{"ann"},
	}

	requests, health := gh.ReviewRequests(search)
	if !health.OK {
		t.Fatalf("health = %+v", health)
	}
	if len(*queries) != 1 {
		t.Fatalf("made %d requests, want 1", len(*queries))
	}
	got := map[string]string{}
	for _, r := range requests {
		got[r.Key()] = r.AckSubject
	}
	want := map[string]string{
		"o/r#7/requested/RR_me": "PR_1",
		"o/r#8/label/LE_1":      "PR_2",
		"o/r#9/comment/C_ask":   "C_ask",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for key, subject := range want {
		if got[key] != subject {
			t.Errorf("%s acks %q, want %q (all: %v)", key, got[key], subject, got)
		}
	}
}

// Label and comment searches are scoped to orgs; without any, only review requests are asked.
func TestReviewSearchWithoutOrgsAsksOnlyForRequests(t *testing.T) {
	query, ok := reviewQuery(ReviewSearch{
		Triggers: []Trigger{TriggerRequested, TriggerLabel, TriggerComment},
		Label:    "gate-review", Command: "/gate review",
	}, "me")
	if !ok || !strings.Contains(query, "review-requested:me") || strings.Contains(query, "label:") || strings.Contains(query, "in:comments") {
		t.Fatalf("query = %s", query)
	}
}

func TestReviewRequestsIgnoreUnrelatedTeamEvents(t *testing.T) {
	response := strings.Replace(reviewSearchResponse,
		`"requestedReviewer":{"__typename":"User","login":"me"}}]`,
		`"requestedReviewer":{"__typename":"User","login":"me"}},
		{"__typename":"ReviewRequestedEvent","id":"RR_team","requestedReviewer":{"__typename":"Team"}}]`, 1)
	gh, _ := githubReturning(t, response)
	gh.login = "me"
	asks, health := gh.ReviewRequests(ReviewSearch{Triggers: []Trigger{TriggerRequested}})
	if !health.OK || len(asks) != 1 || asks[0].TriggerID != "RR_me" {
		t.Fatalf("asks = %+v, health = %+v", asks, health)
	}
}

func TestReviewRequestsRetainIdentityBeyondTimelineWindow(t *testing.T) {
	for _, trigger := range []Trigger{TriggerRequested, TriggerLabel} {
		t.Run(string(trigger), func(t *testing.T) {
			event := `{"__typename":"ReviewRequestedEvent","id":"ask","requestedReviewer":{"__typename":"User","login":"me"}}`
			if trigger == TriggerLabel {
				event = `{"__typename":"LabeledEvent","id":"ask","label":{"name":"gate-review"}}`
			}
			responses := []string{
				fmt.Sprintf(`{"data":{"viewer":{"login":"me"},%q:{"nodes":[{
					"id":"PR_1","number":7,"title":"Keep metadata","repository":{"nameWithOwner":"o/r"},
					"timelineItems":{"pageInfo":{"hasPreviousPage":true,"startCursor":"recent"},"nodes":[]}
				}]}}}`, trigger),
				`{"data":{"node":{"timelineItems":{"pageInfo":{"hasPreviousPage":true,"startCursor":"older"},
					"nodes":[{"__typename":"ReviewRequestedEvent","id":"team","requestedReviewer":{"__typename":"Team"}}]}}}}`,
				fmt.Sprintf(`{"data":{"node":{"timelineItems":{"pageInfo":{"hasPreviousPage":false},"nodes":[%s]}}}}`, event),
			}
			calls := 0
			gh := &GitHub{login: "me", Run: func(args ...string) ([]byte, error) {
				if calls >= len(responses) {
					t.Fatal("pagination did not stop")
				}
				if calls > 0 {
					cursor := []string{"recent", "older"}[calls-1]
					if !strings.Contains(strings.Join(args, " "), fmt.Sprintf("before: %q", cursor)) {
						t.Fatalf("query does not use cursor %q: %v", cursor, args)
					}
				}
				response := responses[calls]
				calls++
				return []byte(response), nil
			}}
			asks, health := gh.ReviewRequests(ReviewSearch{Triggers: []Trigger{trigger}, Orgs: []string{"o"}, Label: "gate-review"})
			if !health.OK || len(asks) != 1 || asks[0].Key() != fmt.Sprintf("o/r#7/%s/ask", trigger) || asks[0].Title != "Keep metadata" {
				t.Fatalf("asks = %+v, health = %+v", asks, health)
			}
			if calls != 3 {
				t.Fatalf("made %d calls, want 3", calls)
			}
		})
	}
}

func TestReviewRequestsDoNotInventIdentity(t *testing.T) {
	for _, response := range []string{
		`{"data":{"node":{"timelineItems":{"nodes":[{"__typename":"ReviewRequestedEvent","id":"team","requestedReviewer":{"__typename":"Team"}}]}}}}`,
		`{"errors":[{"message":"permission denied"}]}`,
		`{"data":{"node":null}}`,
		`{"data":{"node":{"timelineItems":{"pageInfo":{"hasPreviousPage":true,"startCursor":"recent"},"nodes":[{}]}}}}`,
	} {
		t.Run(response, func(t *testing.T) {
			calls := 0
			gh := &GitHub{login: "me", Run: func(args ...string) ([]byte, error) {
				calls++
				if calls == 1 {
					return []byte(`{"data":{"viewer":{"login":"me"},"requested":{"nodes":[{
						"id":"PR_1","number":7,"timelineItems":{"pageInfo":{"hasPreviousPage":true,"startCursor":"recent"}}
					}]}}}`), nil
				}
				if calls > 2 {
					t.Fatal("pagination did not stop")
				}
				return []byte(response), nil
			}}
			asks, health := gh.ReviewRequests(ReviewSearch{Triggers: []Trigger{TriggerRequested}})
			if len(asks) != 0 || calls != 2 {
				t.Fatalf("asks = %+v, calls = %d", asks, calls)
			}
			if strings.Contains(response, `"id":"team"`) != health.OK {
				t.Fatalf("health = %+v", health)
			}
		})
	}
}
