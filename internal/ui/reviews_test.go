package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func prepareTestBriefing(t *testing.T, m *Model) store.ReviewRequest {
	t.Helper()
	m.cfg.Integrations.Reviews.Tool = "claude"
	m.cfg.Integrations.Reviews.Directory = t.TempDir()
	m.cfg.Integrations.Reviews.Prompt = "Brief me on {head}"
	m.reviewQueue, m.reviewsTab = true, true
	ask := store.ReviewRequest{
		Repo: "example/api", Number: 7, Trigger: "requested", TriggerID: "R_1",
		Title: "Make writes idempotent", HeadSHA: "aaaaaaa", TriggeredAt: time.Now(),
	}
	if _, err := m.store.UpsertReviewRequests([]store.ReviewRequest{ask}, time.Now()); err != nil {
		t.Fatal(err)
	}
	id, err := m.launchReview(ask)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.AttachReviewSession(ask.Repo, ask.Number, id, ask.HeadSHA, time.Now()); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.loadReviews())
	for i := range m.sessions {
		if m.sessions[i].ID == id {
			m.sessions[i].Status = status.Finished
			break
		}
	}
	return m.reviews[0]
}

func ingestTestBriefingHead(t *testing.T, m *Model, ask store.ReviewRequest, head string) {
	t.Helper()
	ask.HeadSHA = head
	if _, err := m.store.UpsertReviewRequests([]store.ReviewRequest{ask}, time.Now()); err != nil {
		t.Fatal(err)
	}
	m.finishReviews(reviewsMsg{ingested: true, health: forge.Health{OK: true}, found: 1})
}

func TestReviewStartRefreshesAStaleBriefing(t *testing.T) {
	for _, surface := range []string{"queue", "pull request page"} {
		t.Run(surface, func(t *testing.T) {
			m := buildModel(t)
			old := prepareTestBriefing(t, m)
			if got := m.reviewTabLines(120, 2)[1].text; !strings.Contains(got, "briefing ready") {
				t.Fatalf("initial review row = %q; want briefing ready", got)
			}

			const head = "bbbbbbb"
			if surface == "queue" {
				ingestTestBriefingHead(t, m, old, head)
			} else {
				_ = m.openPullRequest(old)
				m.finishPullRequest(prDetailMsg{
					key: prDetailKey(old), previousHead: old.HeadSHA, health: forge.Health{OK: true},
					detail: &forge.PRDetail{Repo: old.Repo, Number: old.Number, HeadSHA: head},
				})
				var header strings.Builder
				for _, line := range m.reviewsPR().renderPage(100, 24) {
					for _, span := range line {
						header.WriteString(span.Text)
					}
				}
				if got := header.String(); !strings.Contains(got, "press s to refresh briefing") {
					t.Errorf("PR page = %q; want a refresh hint", got)
				}
			}
			if got := m.reviewTabLines(120, 2)[1].text; !strings.Contains(got, "briefing stale") || strings.Contains(got, "briefing ready") {
				t.Errorf("review row at %s = %q; want briefing stale", head, got)
			}
			if got := m.reviews[0]; got.SessionID != old.SessionID || got.SessionHead != old.HeadSHA {
				t.Fatalf("fetch replaced the old briefing: %+v", got)
			}

			m.handleKey(tea.KeyPressMsg{Code: 's', Text: "s"})
			got := m.reviews[0]
			if got.SessionID == old.SessionID || got.SessionHead != head || got.HeadSHA != head || got.State != store.ReviewStarted {
				t.Fatalf("after pressing s, review = %+v; want a new session on %s", got, head)
			}
			if !m.tmux.Exists(old.SessionID) || !m.tmux.Exists(got.SessionID) {
				t.Fatal("refresh should keep the old session and launch a new one")
			}
			if sess, _ := m.reviewSession(got); sess == nil || !strings.Contains(sess.LaunchPrompt, "Brief me on "+head) {
				t.Fatal("the new session was not asked to brief the new head")
			}
			m.reviewsTabAction(keymap.ReviewStart)
			if m.reviews[0].SessionID != got.SessionID {
				t.Fatal("starting again should reuse the current session")
			}
		})
	}
}

func TestReviewPageDoesNotRestoreAnOlderHeadAfterIngest(t *testing.T) {
	m := buildModel(t)
	old := prepareTestBriefing(t, m)
	_ = m.openPullRequest(old)
	ingestTestBriefingHead(t, m, old, "ccccccc")

	// The page request started on A and returned B after the queue learned C.
	m.finishPullRequest(prDetailMsg{
		key: prDetailKey(old), previousHead: old.HeadSHA, health: forge.Health{OK: true},
		detail: &forge.PRDetail{Repo: old.Repo, Number: old.Number, HeadSHA: "bbbbbbb"},
	})
	m.handleKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	if got := m.reviews[0]; got.HeadSHA != "ccccccc" || got.SessionHead != "ccccccc" {
		t.Fatalf("after pressing s, review = %+v; want a new session on the ingested head", got)
	}
}

func TestReviewRefreshKeepsOldSessionIfLaunchFails(t *testing.T) {
	m := buildModel(t)
	old := prepareTestBriefing(t, m)
	ingestTestBriefingHead(t, m, old, "bbbbbbb")
	m.cfg.Integrations.Reviews.Directory = filepath.Join(t.TempDir(), "missing")

	m.handleKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	got := m.reviews[0]
	if got.State != store.ReviewPending || got.SessionID != old.SessionID || got.SessionHead != old.HeadSHA ||
		!m.reviewsTab || !strings.Contains(m.errBar.text, "working directory does not exist") {
		t.Fatalf("failed refresh should keep the old attachment and report the error: review=%+v, reviews tab=%v, error=%q",
			got, m.reviewsTab, m.errBar.text)
	}
}
