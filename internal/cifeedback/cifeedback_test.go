package cifeedback

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/workspec"
)

const prURL = "https://github.com/example/repo/pull/7"

func feedbackStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "board.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateSession(store.Session{ID: "owner", Tool: "claude", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordOpenedPRs("owner", []string{prURL}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return st
}

func failedPR(at time.Time, head, attempt string) forge.PR {
	return forge.PR{URL: prURL, State: forge.PROpen, Checks: forge.ChecksFailing, FetchedAt: at,
		CI: &forge.CISnapshot{HeadSHA: head, Complete: true, Checks: []forge.CICheck{{
			ID: attempt, Name: "test", Status: "COMPLETED", Conclusion: "FAILURE", URL: "https://github.com/example/repo/actions/runs/9",
		}}}}
}

func observe(t *testing.T, st *store.Store, pr forge.PR) {
	t.Helper()
	if err := Observe(st, map[string]forge.PR{"pr": pr}, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
}

func inbox(t *testing.T, st *store.Store) []store.InboxMessage {
	t.Helper()
	messages, err := st.Inbox("owner", store.InboxFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return messages
}

func TestFeedbackSurvivesReopenAndBoundsRoundsAcrossCommits(t *testing.T) {
	st := feedbackStore(t)
	at := time.Now()
	pr := failedPR(at, "head-a", "run-a")
	observe(t, st, pr)
	first, ok, err := st.HeadMessage("owner")
	if err != nil || !ok || first.Interrupt || first.SenderID != store.SystemSenderID || !strings.Contains(first.Body, "head-a") {
		t.Fatalf("first feedback = %+v, %v, %v", first, ok, err)
	}
	reopened, err := st.Reopen()
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pr.FetchedAt = at.Add(time.Minute)
	observe(t, reopened, pr)
	if got := len(inbox(t, st)); got != 1 {
		t.Fatalf("replayed failure after reopening: %d messages", got)
	}
	if err := st.MarkDelivered(first.ID, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	pr = failedPR(at.Add(2*time.Minute), "head-b", "run-b")
	observe(t, st, pr)
	second, ok, err := st.HeadMessage("owner")
	if err != nil || !ok || !strings.Contains(second.Body, "round 2 of 2") {
		t.Fatalf("second feedback = %+v, %v, %v", second, ok, err)
	}
	if err := st.MarkDelivered(second.ID, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pr = failedPR(at.Add(3*time.Minute), "head-c", "run-c")
	observe(t, st, pr)
	msg, ok, err := st.HeadMessage("owner")
	if err != nil || !ok || !strings.Contains(msg.Body, "paused") {
		t.Fatalf("missing escalation after two rounds: %+v, %v, %v", msg, ok, err)
	}
	observe(t, st, failedPR(at.Add(4*time.Minute), "head-d", "run-d"))
	if got := len(inbox(t, st)); got != 3 {
		t.Fatalf("repair loop continued after escalation: %d messages", got)
	}
	for i, state := range []string{"incomplete", "pending"} {
		unknown := failedPR(at.Add(4*time.Minute+time.Duration(i+1)*time.Second), "head-d", "run-d")
		if state == "incomplete" {
			unknown.CI.Complete = false
		} else {
			unknown.CI.Checks[0].Status, unknown.CI.Checks[0].Conclusion = "IN_PROGRESS", ""
		}
		observe(t, st, unknown)
		pause, ok, err := st.HeadMessage("owner")
		if err != nil || !ok || pause.ID != msg.ID {
			t.Fatalf("%s evidence lost the episode pause notice: %+v, %v, %v", state, pause, ok, err)
		}
	}
	green := failedPR(at.Add(5*time.Minute), "head-d", "run-d")
	green.Checks, green.CI.Checks[0].Conclusion = forge.ChecksPassing, "SUCCESS"
	observe(t, st, green)
	if _, ok, err := st.HeadMessage("owner"); err != nil || ok {
		t.Fatalf("green left obsolete feedback queued: %v, %v", ok, err)
	}
	observe(t, st, failedPR(at.Add(6*time.Minute), "head-e", "run-e"))
	msg, ok, err = st.HeadMessage("owner")
	if err != nil || !ok || !strings.Contains(msg.Body, "round 1 of 2") {
		t.Fatalf("green did not reset episode: %+v, %v, %v", msg, ok, err)
	}
	observe(t, st, failedPR(at.Add(time.Second), "head-old", "run-old"))
	if got := len(inbox(t, st)); got != 3 {
		t.Fatalf("older response replayed: %d messages", got)
	}
}

func TestFeedbackRequiresUnambiguousLiveProvenance(t *testing.T) {
	for _, scenario := range []string{"mentioned", "two-owners", "dead", "shell"} {
		t.Run(scenario, func(t *testing.T) {
			st := feedbackStore(t)
			pr := failedPR(time.Now(), "head", "run")
			switch scenario {
			case "mentioned":
				pr.URL += "0"
			case "two-owners":
				if err := st.CreateSession(store.Session{ID: "other", Tool: "claude", Status: "idle"}); err != nil {
					t.Fatal(err)
				}
				if err := st.RecordOpenedPRs("other", []string{prURL}, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "dead":
				if err := st.UpdateStatus("owner", "dead"); err != nil {
					t.Fatal(err)
				}
			case "shell":
				if err := Observe(st, map[string]forge.PR{"pr": pr}, []string{"codex"}); err != nil {
					t.Fatal(err)
				}
				if got := len(inbox(t, st)); got != 0 {
					t.Fatalf("sent to a shell: %d messages", got)
				}
				return
			}
			observe(t, st, pr)
			if got := len(inbox(t, st)); got != 0 {
				t.Fatalf("sent without a unique live owner: %d messages", got)
			}
		})
	}
}

func TestFeedbackIgnoresIncompleteAndNonFailureResults(t *testing.T) {
	for _, scenario := range []string{"pending", "cancelled", "action-required", "truncated", "draft", "closed", "cache"} {
		t.Run(scenario, func(t *testing.T) {
			st := feedbackStore(t)
			pr := failedPR(time.Now(), "head", "run")
			switch scenario {
			case "pending":
				pr.CI.Checks = append(pr.CI.Checks, forge.CICheck{ID: "slow", Status: "IN_PROGRESS"})
			case "cancelled":
				pr.CI.Checks[0].Conclusion = "CANCELLED"
			case "action-required":
				pr.CI.Checks[0].Conclusion = "ACTION_REQUIRED"
			case "truncated":
				pr.CI.Complete = false
			case "draft":
				pr.State = forge.PRDraft
			case "closed":
				pr.State = forge.PRClosed
			case "cache":
				pr.CI = nil
			}
			observe(t, st, pr)
			if got := len(inbox(t, st)); got != 0 {
				t.Fatalf("spurious repair notification: %d messages", got)
			}
		})
	}
}

func TestFullQueueDoesNotLoseFailureOrSpendRound(t *testing.T) {
	st := feedbackStore(t)
	at := time.Now()
	for i := range store.DefaultInboxLimits.QueueCap {
		_, _, err := st.Enqueue(store.InboxMessage{SessionID: "owner", SenderID: fmt.Sprint(i), Fingerprint: fmt.Sprint(i), Body: "other", SentAt: at}, store.DefaultInboxLimits)
		if err != nil {
			t.Fatal(err)
		}
	}
	pr := failedPR(at, "head", "run")
	if err := Observe(st, map[string]forge.PR{"pr": pr}, []string{"claude"}); !errors.Is(err, store.ErrInboxFull) {
		t.Fatalf("full queue = %v", err)
	}
	first, _, _ := st.HeadMessage("owner")
	if err := st.MarkDelivered(first.ID, at); err != nil {
		t.Fatal(err)
	}
	observe(t, st, pr)
	for _, msg := range inbox(t, st) {
		if msg.SenderID == store.SystemSenderID {
			if !strings.Contains(msg.Body, "round 1 of 2") {
				t.Fatalf("failed enqueue consumed round: %s", msg.Body)
			}
			return
		}
	}
	t.Fatal("failure lost when queue was full")
}

func TestGitHubCurrentHeadAndRerunIdentityReachInbox(t *testing.T) {
	st := feedbackStore(t)
	at := time.Now()
	head, commit, attempt := "new-head", "old-head", "run-1"
	gh := &forge.GitHub{Now: func() time.Time { return at }, Run: func(...string) ([]byte, error) {
		return fmt.Appendf(nil, `{"data":{"p0":{"pullRequest":{"number":7,"url":%q,"state":"OPEN","headRefOid":%q,"commits":{"nodes":[{"commit":{"oid":%q,"statusCheckRollup":{"state":"FAILURE","contexts":{"pageInfo":{"hasPreviousPage":false},"nodes":[{"id":%q,"name":"tests","status":"COMPLETED","conclusion":"FAILURE","detailsUrl":"https://github.com/example/repo/actions/runs/9"}]}}}}]}}}}}`, prURL, head, commit, attempt), nil
	}}
	ref := []workspec.Ref{{Kind: workspec.KindPR, Repo: "example/repo", Number: 7}}
	read := func() {
		t.Helper()
		prs, health := gh.PRs(ref)
		if !health.OK || len(prs) != 1 {
			t.Fatalf("GitHub response: %+v, %+v", prs, health)
		}
		if err := Observe(st, prs, []string{"claude"}); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if got := len(inbox(t, st)); got != 0 {
		t.Fatalf("old commit produced feedback: %d", got)
	}
	commit = head
	read()
	first, ok, err := st.HeadMessage("owner")
	if err != nil || !ok {
		t.Fatalf("missing current-head failure: %v, %v", ok, err)
	}
	if err := st.MarkDelivered(first.ID, at); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	attempt = "run-2"
	read()
	if got := len(inbox(t, st)); got != 2 {
		t.Fatalf("rerun identity lost: %d messages", got)
	}
}

func TestPendingCommitRetiresOnlyUnclaimedFeedback(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(fmt.Sprint(claimed), func(t *testing.T) {
			st := feedbackStore(t)
			at := time.Now()
			observe(t, st, failedPR(at, "old-head", "run-1"))
			first, ok, err := st.HeadMessage("owner")
			if err != nil || !ok {
				t.Fatalf("missing feedback: %v, %v", ok, err)
			}
			if claimed {
				if ok, err := st.ClaimMessage(first.ID, at); err != nil || !ok {
					t.Fatalf("claim = %v, %v", ok, err)
				}
			}
			pending := failedPR(at.Add(time.Minute), "new-head", "run-2")
			pending.CI.Complete = false
			observe(t, st, pending)
			msg, ok, err := st.HeadMessage("owner")
			if err != nil || ok != claimed || (ok && msg.ID != first.ID) {
				t.Fatalf("pending head changed claimed delivery: %+v, %v, %v", msg, ok, err)
			}
		})
	}
}

func TestRerunOfSameCheckIDWithNewCompletionTimeIsNewFeedback(t *testing.T) {
	st := feedbackStore(t)
	at := time.Now()
	pr := failedPR(at, "same-head", "same-run")
	pr.CI.Checks[0].CompletedAt = at
	observe(t, st, pr)
	first, ok, err := st.HeadMessage("owner")
	if err != nil || !ok {
		t.Fatalf("missing feedback: %v, %v", ok, err)
	}
	if err := st.MarkDelivered(first.ID, at); err != nil {
		t.Fatal(err)
	}
	pr.FetchedAt = at.Add(time.Minute)
	pr.CI.Checks[0].CompletedAt = pr.FetchedAt
	observe(t, st, pr)
	msg, ok, err := st.HeadMessage("owner")
	if err != nil || !ok || !strings.Contains(msg.Body, "round 2 of 2") {
		t.Fatalf("rerun of same check was lost: %+v, %v, %v", msg, ok, err)
	}
}

func TestUnchangedFailureReturnsAfterAnotherCheckReruns(t *testing.T) {
	for _, delivery := range []string{"queued", "claimed", "delivered"} {
		t.Run(delivery, func(t *testing.T) {
			st := feedbackStore(t)
			at := time.Now()
			pr := failedPR(at, "head", "failed-run")
			pr.CI.Checks = append(pr.CI.Checks, forge.CICheck{ID: "other-run", Status: "COMPLETED", Conclusion: "SUCCESS"})
			observe(t, st, pr)
			first, ok, err := st.HeadMessage("owner")
			if err != nil || !ok {
				t.Fatalf("initial feedback: %v, %v", ok, err)
			}
			switch delivery {
			case "claimed":
				if ok, err := st.ClaimMessage(first.ID, at); err != nil || !ok {
					t.Fatalf("claim: %v, %v", ok, err)
				}
			case "delivered":
				if err := st.MarkDelivered(first.ID, at); err != nil {
					t.Fatal(err)
				}
			}
			pr.FetchedAt = at.Add(time.Minute)
			pr.CI.Checks[1].Status, pr.CI.Checks[1].Conclusion = "IN_PROGRESS", ""
			observe(t, st, pr)
			reopened, err := st.Reopen()
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			pr.FetchedAt = at.Add(2 * time.Minute)
			pr.CI.Checks[1].Status, pr.CI.Checks[1].Conclusion = "COMPLETED", "SUCCESS"
			observe(t, reopened, pr)
			msg, ok, err := reopened.HeadMessage("owner")
			if err != nil {
				t.Fatal(err)
			}
			switch delivery {
			case "claimed":
				if !ok || msg.ID != first.ID || len(inbox(t, reopened)) != 1 {
					t.Fatalf("claimed feedback duplicated: %+v, %v", msg, ok)
				}
			case "delivered":
				if ok || len(inbox(t, reopened)) != 1 {
					t.Fatalf("delivered feedback replayed: %+v, %v", msg, ok)
				}
			case "queued":
				if !ok || msg.ID == first.ID || !strings.Contains(msg.Body, "round 1 of 2") {
					t.Fatalf("retired failure did not return in its original round: %+v, %v", msg, ok)
				}
				if err := reopened.MarkDelivered(msg.ID, pr.FetchedAt); err != nil {
					t.Fatal(err)
				}
				observe(t, reopened, failedPR(at.Add(3*time.Minute), "next-head", "next-run"))
				next, ok, err := reopened.HeadMessage("owner")
				if err != nil || !ok || !strings.Contains(next.Body, "round 2 of 2") {
					t.Fatalf("redelivery spent another round: %+v, %v, %v", next, ok, err)
				}
			}
		})
	}
}
