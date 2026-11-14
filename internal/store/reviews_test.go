package store

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/priority"
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
	if err := s.SetPullRequestReviewState("o/r", 7, ReviewDismissed, "", "", now); err != nil {
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
	late, err := s.AddReviewDraft(ReviewDraft{Repo: "o/r", Number: 7, Path: "a.go", Line: 4, Body: "and here?", Author: DraftByAgent}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishReviewDrafts(drafts, now); err != nil {
		t.Fatal(err)
	}
	if drafts, _ := s.ReviewDrafts("o/r", 7); len(drafts) != 1 || drafts[0].ID != late.ID {
		t.Fatalf("drafts after submit = %+v, want only the one written during it", drafts)
	}
	if err := s.SetReviewDraft(d.ID, DraftDropped, "", now); err == nil {
		t.Fatal("a submitted draft changed state")
	}
}

// A session prepared ahead of the operator is recorded on the ask without starting it, so the ask
// still queues as waiting until the operator opens it, and keeps the head it was started on.
func TestAttachReviewSessionKeepsTheAskPending(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1700000000, 0)
	ask := ReviewRequest{Repo: "o/r", Number: 7, Trigger: "requested", TriggerID: "R_1", HeadSHA: "aaa"}
	if _, err := s.UpsertReviewRequests([]ReviewRequest{ask}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachReviewSession("o/r", 7, "sess-1", "aaa", now); err != nil {
		t.Fatal(err)
	}
	ask.HeadSHA = "bbb"
	if _, err := s.UpsertReviewRequests([]ReviewRequest{ask}, now); err != nil {
		t.Fatal(err)
	}
	open, err := s.OpenReviewRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].State != ReviewPending || open[0].SessionID != "sess-1" || open[0].SessionHead != "aaa" {
		t.Fatalf("open = %+v, want one pending ask on sess-1 still on the head it was started on", open)
	}
	if err := s.SetPullRequestReviewState("o/r", 7, ReviewStarted, "sess-1", "bbb", now); err != nil {
		t.Fatal(err)
	}
	if open, err = s.OpenReviewRequests(); err != nil || open[0].SessionHead != "aaa" {
		t.Fatalf("open = %+v, err %v; opening the prepared session moved its head", open, err)
	}
}

func TestReviewIngestRefreshesHeadForOtherOpenAsks(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1700000000, 0)
	old := ReviewRequest{Repo: "o/r", Number: 7, Trigger: "requested", TriggerID: "R_1", HeadSHA: "aaa"}
	if _, err := s.UpsertReviewRequests([]ReviewRequest{old}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachReviewSession(old.Repo, old.Number, "sess-1", old.HeadSHA, now); err != nil {
		t.Fatal(err)
	}
	newAsk := ReviewRequest{Repo: old.Repo, Number: old.Number, Trigger: "comment", TriggerID: "C_1", HeadSHA: "bbb"}
	if _, err := s.UpsertReviewRequests([]ReviewRequest{newAsk}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	open, err := s.OpenReviewRequests()
	if err != nil || len(open) != 2 {
		t.Fatalf("open = %+v, err = %v; want two asks", open, err)
	}
	for _, ask := range open {
		if ask.HeadSHA != "bbb" {
			t.Errorf("ask %s head = %q; want latest head bbb", ask.TriggerID, ask.HeadSHA)
		}
		if ask.TriggerID == old.TriggerID && ask.SessionHead != "aaa" {
			t.Errorf("session head = %q; want the original head aaa", ask.SessionHead)
		}
	}
}

func TestReviewSourceFollowsTheActiveConversationAndSurvivesArchival(t *testing.T) {
	st := newTestStore(t)
	now := time.Unix(1700000000, 0)
	url := "https://github.com/example/api/pull/7"
	for i, id := range []string{"original", "resumed"} {
		sess := sample(id, "release")
		sess.CreatedAt = now.Add(time.Duration(i) * time.Minute)
		sess.Priority = priority.High
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		if err := st.RecordOpenedPRs(id, []string{url}, now); err != nil {
			t.Fatal(err)
		}
	}
	ask := ReviewRequest{Repo: "example/api", Number: 7, Trigger: "session", TriggerID: "pr:example/api#7", URL: url}
	if _, err := st.UpsertReviewRequests([]ReviewRequest{ask}, now); err != nil {
		t.Fatal(err)
	}
	assertSource := func(id string) {
		t.Helper()
		open, err := st.OpenReviewRequests()
		if err != nil || len(open) != 1 {
			t.Fatalf("open = %+v, err = %v", open, err)
		}
		if open[0].OriginSessionID != id || open[0].OriginSession.Priority != priority.High || open[0].OriginSession.Group != "release" {
			t.Fatalf("origin = %+v; want %s with its priority and group", open[0].OriginSession, id)
		}
	}
	assertSource("resumed")
	if err := st.SetArchived("resumed", true); err != nil {
		t.Fatal(err)
	}
	assertSource("original")
	if err := st.SetArchived("original", true); err != nil {
		t.Fatal(err)
	}
	assertSource("resumed")
}
