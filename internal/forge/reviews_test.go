package forge

import (
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
