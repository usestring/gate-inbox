package nativefeedback

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/store"
)

const testURL = "https://github.com/example/repo/pull/7"

func board(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateSession(store.Session{ID: "owner", Tool: "agent", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordOpenedPRs("owner", []string{testURL}, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	return st
}

func reviewedPR() forge.PR {
	at := time.Unix(200, 0)
	return forge.PR{URL: testURL, State: forge.PROpen, FetchedAt: at, Feedback: &forge.PRFeedbackSnapshot{
		HeadSHA: "head", BaseSHA: "base", Mergeability: "MERGEABLE", ReviewsComplete: true, ThreadsComplete: true,
		Reviews: []forge.PRReview{{ID: "review", HeadSHA: "head", State: "CHANGES_REQUESTED", Author: "reviewer", AuthorType: "User", SubmittedAt: at}},
	}}
}

func observe(t *testing.T, st *store.Store, pr forge.PR) {
	t.Helper()
	if err := Observe(st, map[string]forge.PR{"pr": pr}, []string{"agent"}); err != nil {
		t.Fatal(err)
	}
}

func head(t *testing.T, st *store.Store, want bool) store.InboxMessage {
	t.Helper()
	msg, ok, err := st.HeadMessage("owner")
	if err != nil || ok != want {
		t.Fatalf("head = %+v, %v, %v; want %v", msg, ok, err, want)
	}
	return msg
}

func TestFeedbackUsesCurrentHumanVerdictsAndConfirmedConflicts(t *testing.T) {
	for _, scenario := range []string{"request", "comment", "bot", "old-head", "approved", "dismissed", "comment-after-request", "thread", "old-thread", "outdated", "resolved", "bot-thread", "truncated-reviews", "truncated-threads", "conflict", "unknown", "missing-base", "draft", "closed", "cache", "ambiguous", "archived", "dead", "mentioned"} {
		t.Run(scenario, func(t *testing.T) {
			st := board(t)
			pr := reviewedPR()
			f := pr.Feedback
			want := false
			switch scenario {
			case "request":
				want = true
			case "comment":
				f.Reviews[0].State = "COMMENTED"
			case "bot":
				f.Reviews[0].AuthorType = "Bot"
			case "old-head":
				f.Reviews[0].HeadSHA = "previous"
			case "approved", "dismissed", "comment-after-request":
				r := f.Reviews[0]
				r.ID = "new"
				r.SubmittedAt = r.SubmittedAt.Add(time.Second)
				r.State = map[string]string{"approved": "APPROVED", "dismissed": "DISMISSED", "comment-after-request": "COMMENTED"}[scenario]
				f.Reviews = append([]forge.PRReview{r}, f.Reviews...)
				want = scenario == "comment-after-request"
			case "thread", "old-thread", "outdated", "resolved", "bot-thread":
				f.Reviews = nil
				f.Threads = []forge.PRThread{{ID: "thread", HeadSHA: "head", AuthorType: "User"}}
				f.Threads[0].Outdated = scenario == "outdated"
				f.Threads[0].Resolved = scenario == "resolved"
				if scenario == "old-thread" {
					f.Threads[0].HeadSHA = "previous"
				}
				if scenario == "bot-thread" {
					f.Threads[0].AuthorType = "Bot"
				}
				want = scenario == "thread"
			case "truncated-reviews":
				f.ReviewsComplete = false
			case "truncated-threads":
				f.ThreadsComplete = false
			case "conflict", "unknown", "missing-base":
				f.Reviews = nil
				f.Mergeability = "CONFLICTING"
				if scenario == "unknown" {
					f.Mergeability = "UNKNOWN"
				}
				if scenario == "missing-base" {
					f.BaseSHA = ""
				}
				want = scenario == "conflict"
			case "draft":
				pr.State = forge.PRDraft
			case "closed":
				pr.State = forge.PRClosed
			case "cache":
				pr.Feedback = nil
			case "ambiguous":
				if err := st.CreateSession(store.Session{ID: "other", Tool: "agent", Status: "idle"}); err != nil {
					t.Fatal(err)
				}
				if err := st.RecordOpenedPRs("other", []string{testURL}, pr.FetchedAt); err != nil {
					t.Fatal(err)
				}
			case "archived":
				if _, err := st.ArchiveSession("owner"); err != nil {
					t.Fatal(err)
				}
			case "dead":
				if err := st.UpdateStatus("owner", "dead"); err != nil {
					t.Fatal(err)
				}
			case "mentioned":
				pr.URL += "0"
			}
			observe(t, st, pr)
			msg := head(t, st, want)
			if want && (msg.Interrupt || msg.SenderID != store.SystemSenderID || !strings.Contains(msg.Fingerprint, ":v1:")) {
				t.Fatalf("feedback bypassed normal system inbox: %+v", msg)
			}
		})
	}
}

func TestReviewBudgetsSurviveReopenHeadChangesAndIncompleteEvidence(t *testing.T) {
	st := board(t)
	pr := reviewedPR()
	pr.Feedback.Threads = []forge.PRThread{{ID: "thread-a", HeadSHA: "head", AuthorType: "User"}, {ID: "thread-b", HeadSHA: "head", AuthorType: "User"}}
	observe(t, st, pr)
	first := head(t, st, true)
	if err := st.MarkDelivered(first.ID, pr.FetchedAt); err != nil {
		t.Fatal(err)
	}
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	slices.Reverse(pr.Feedback.Threads)
	observe(t, reopened, pr)
	head(t, st, false)
	// A new commit cannot count an old review as approval and reset the repair budget.
	pr.Feedback.HeadSHA = "next-head"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	head(t, st, false)
	pr.Feedback.Reviews[0].HeadSHA = "next-head"
	pr.Feedback.Reviews[0].ID = "next-review"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	second := head(t, st, true)
	if !strings.Contains(second.Body, "round 2 of 2") {
		t.Fatalf("lost budget: %s", second.Body)
	}
	if err := st.MarkDelivered(second.ID, pr.FetchedAt); err != nil {
		t.Fatal(err)
	}
	pr.Feedback.ReviewsComplete = false
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	pr.Feedback.ReviewsComplete = true
	pr.Feedback.Reviews[0].ID = "third-review"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	paused := head(t, st, true)
	if !strings.Contains(paused.Body, "paused") {
		t.Fatalf("missing escalation: %s", paused.Body)
	}
	if err := st.MarkDelivered(paused.ID, pr.FetchedAt); err != nil {
		t.Fatal(err)
	}
	pr.Feedback.Reviews[0].ID = "fourth-review"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	head(t, st, false)
	pr.Feedback.Threads = nil
	pr.Feedback.Reviews[0].State = "APPROVED"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	pr.Feedback.Reviews[0].State = "CHANGES_REQUESTED"
	pr.Feedback.Reviews[0].ID = "fresh-review"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	if msg := head(t, st, true); !strings.Contains(msg.Body, "round 1 of 2") {
		t.Fatalf("approval did not reset: %s", msg.Body)
	}
}

func TestConflictBaseChangesAreDeduplicatedIndependentlyAndClaimsArePreserved(t *testing.T) {
	st := board(t)
	pr := reviewedPR()
	pr.Feedback.Mergeability = "CONFLICTING"
	observe(t, st, pr)
	review := head(t, st, true)
	if err := st.MarkDelivered(review.ID, pr.FetchedAt); err != nil {
		t.Fatal(err)
	}
	conflict := head(t, st, true)
	if ok, err := st.ClaimMessage(conflict.ID, pr.FetchedAt); err != nil || !ok {
		t.Fatal(err)
	}
	pr.Feedback.Mergeability = "UNKNOWN"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	if msg := head(t, st, true); msg.ID != conflict.ID {
		t.Fatal("unknown changed a claimed delivery")
	}
	if err := st.MarkDelivered(conflict.ID, pr.FetchedAt); err != nil {
		t.Fatal(err)
	}
	pr.Feedback.Mergeability = "CONFLICTING"
	pr.Feedback.BaseSHA = "next-base"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	msg := head(t, st, true)
	if !strings.Contains(msg.Body, "round 2 of 2") || msg.Fingerprint == conflict.Fingerprint {
		t.Fatalf("base change lost: %+v", msg)
	}
	pr.Feedback.Mergeability = "UNKNOWN"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	head(t, st, false)
	pr.Feedback.Mergeability = "CONFLICTING"
	pr.Feedback.BaseSHA = "third-base"
	pr.FetchedAt = pr.FetchedAt.Add(time.Second)
	observe(t, st, pr)
	if msg := head(t, st, true); !strings.Contains(msg.Body, "paused") {
		t.Fatalf("unknown reset budget: %s", msg.Body)
	}
}

func TestQueueFailureDoesNotSpendFeedbackBudget(t *testing.T) {
	st := board(t)
	pr := reviewedPR()
	for i := range store.DefaultInboxLimits.QueueCap {
		if _, _, err := st.Enqueue(store.InboxMessage{SessionID: "owner", SenderID: fmt.Sprint(i), Fingerprint: fmt.Sprint(i), Body: "other", SentAt: pr.FetchedAt}, store.DefaultInboxLimits); err != nil {
			t.Fatal(err)
		}
	}
	if err := Observe(st, map[string]forge.PR{"pr": pr}, []string{"agent"}); !errors.Is(err, store.ErrInboxFull) {
		t.Fatalf("full queue: %v", err)
	}
	msg := head(t, st, true)
	if err := st.MarkDelivered(msg.ID, pr.FetchedAt); err != nil {
		t.Fatal(err)
	}
	observe(t, st, pr)
	messages, err := st.Inbox("owner", store.InboxFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		if msg.SenderID == store.SystemSenderID {
			if !strings.Contains(msg.Body, "round 1 of 2") {
				t.Fatalf("spent round on failed enqueue: %s", msg.Body)
			}
			return
		}
	}
	t.Fatal("lost feedback after full queue")
}

func TestQueuedEscalationSurvivesChangedAndUncertainSnapshotsUntilResolved(t *testing.T) {
	for _, kind := range []string{"review", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			st := board(t)
			pr := reviewedPR()
			if kind == "conflict" {
				pr.Feedback.Reviews = nil
				pr.Feedback.Mergeability = "CONFLICTING"
			}
			for round := range 3 {
				pr.Feedback.HeadSHA = fmt.Sprint("head-", round)
				if kind == "review" {
					pr.Feedback.Reviews[0].HeadSHA = pr.Feedback.HeadSHA
				}
				pr.FetchedAt = pr.FetchedAt.Add(time.Second)
				observe(t, st, pr)
				msg := head(t, st, true)
				if round < 2 {
					if err := st.MarkDelivered(msg.ID, pr.FetchedAt); err != nil {
						t.Fatal(err)
					}
				}
			}
			pause := head(t, st, true)
			if !strings.Contains(pause.Body, "paused") {
				t.Fatal("missing pause notice")
			}
			pr.Feedback.HeadSHA = "changed-head"
			pr.Feedback.BaseSHA = "changed-base"
			if kind == "review" {
				pr.Feedback.Reviews[0].HeadSHA = pr.Feedback.HeadSHA
			}
			pr.FetchedAt = pr.FetchedAt.Add(time.Second)
			observe(t, st, pr)
			if msg := head(t, st, true); msg.ID != pause.ID {
				t.Fatal("changed head lost the pause notice")
			}
			pr.Feedback.Mergeability = "UNKNOWN"
			pr.Feedback.ReviewsComplete = false
			pr.FetchedAt = pr.FetchedAt.Add(time.Second)
			observe(t, st, pr)
			if msg := head(t, st, true); msg.ID != pause.ID {
				t.Fatal("uncertainty lost the pause notice")
			}
			pr.Feedback.Mergeability = "MERGEABLE"
			pr.Feedback.ReviewsComplete = true
			pr.Feedback.Reviews = nil
			pr.FetchedAt = pr.FetchedAt.Add(time.Second)
			observe(t, st, pr)
			head(t, st, false)
		})
	}
}
