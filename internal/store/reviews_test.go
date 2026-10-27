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

// Once a review goes out its drafts are done with, so a later ask on the same pull request opens
// with none of them.
func TestSubmittedDraftsLeaveThePullRequest(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1700000000, 0)
	d, err := s.AddReviewDraft(ReviewDraft{Repo: "o/r", Number: 7, Path: "a.go", Line: 3, Body: "why?", Author: DraftByAgent}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetReviewDraft(d.ID, DraftKept, "why here?", now); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.ReviewDrafts("o/r", 7)
	if len(drafts) != 1 || drafts[0].State != DraftKept || drafts[0].Body != "why here?" || drafts[0].Side != "RIGHT" {
		t.Fatalf("drafts = %+v", drafts)
	}
	if err := s.FinishReviewDrafts("o/r", 7, now); err != nil {
		t.Fatal(err)
	}
	if drafts, _ := s.ReviewDrafts("o/r", 7); len(drafts) != 0 {
		t.Fatalf("drafts after submit = %+v", drafts)
	}
	if err := s.SetReviewDraft(d.ID, DraftDropped, "", now); err == nil {
		t.Fatal("a submitted draft changed state")
	}
}

// A session prepared ahead of the operator is recorded on the ask without starting it, so the ask
// still queues as waiting until the operator opens it.
func TestAttachReviewSessionKeepsTheAskPending(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1700000000, 0)
	ask := ReviewRequest{Repo: "o/r", Number: 7, Trigger: "requested", TriggerID: "R_1"}
	if _, err := s.UpsertReviewRequests([]ReviewRequest{ask}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachReviewSession("o/r", 7, "sess-1", now); err != nil {
		t.Fatal(err)
	}
	open, err := s.OpenReviewRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].State != ReviewPending || open[0].SessionID != "sess-1" {
		t.Fatalf("open = %+v, want one pending ask on sess-1", open)
	}
}
