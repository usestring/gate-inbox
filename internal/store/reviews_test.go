package store

import (
	"testing"
	"time"
)

// Finding an ask again never reopens one the operator closed; a new ask on the same pull request
// queues again.
func TestReviewIngestDoesNotResurrectClosedAsks(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1700000000, 0)
	ask := ReviewRequest{Repo: "o/r", Number: 7, Trigger: "comment", TriggerID: "C_1", Title: "Fix"}

	added, err := s.UpsertReviewRequests([]ReviewRequest{ask}, now)
	if err != nil || len(added) != 1 {
		t.Fatalf("first ingest added %d, err %v", len(added), err)
	}
	if err := s.SetPullRequestReviewState("o/r", 7, ReviewDismissed, "", now); err != nil {
		t.Fatal(err)
	}

	ask.Title = "Fix, renamed"
	again := ask
	again.TriggerID = "C_2"
	added, err = s.UpsertReviewRequests([]ReviewRequest{ask, again}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].TriggerID != "C_2" {
		t.Fatalf("second ingest added %+v, want only C_2", added)
	}
	open, err := s.OpenReviewRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].TriggerID != "C_2" {
		t.Fatalf("open = %+v, want only C_2", open)
	}
}
