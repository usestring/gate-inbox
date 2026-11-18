package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

func TestSessionReviewHandoffLifecycle(t *testing.T) {
	m := buildModel(t)
	now := time.Unix(1700000000, 0)
	sess := store.Session{ID: "author", Name: "Implement search", Tool: "claude", Cwd: "/repo", CreatedAt: now, Priority: priority.High}
	if err := m.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/example/api/pull/7"
	m.reviewQueue = true
	m.sessions = []store.Session{sess}
	m.sessions[0].LaunchPrompt = url
	refresh := func(state forge.PRState, recorded, verified bool) []store.ReviewRequest {
		t.Helper()
		if recorded {
			if err := m.store.RecordOpenedPRs(sess.ID, []string{url}, now); err != nil {
				t.Fatal(err)
			}
		}
		m.work = worktracker.New(fakeGit{}, fakePRs{
			prs:    map[string]forge.PR{"pr:example/api#7": {Repo: "example/api", Number: 7, URL: url, Title: "Add search", State: state}},
			health: forge.Health{OK: verified},
		}, nil)
		msg := m.refreshWork()().(workLoadedMsg)
		if msg.reviewsErr != nil {
			t.Fatal(msg.reviewsErr)
		}
		m.update(msg)
		return m.reviews
	}
	if got := refresh(forge.PROpen, false, true); len(got) != 0 {
		t.Fatal("a mentioned PR graduated as authored work")
	}
	if got := refresh(forge.PROpen, true, false); len(got) != 0 {
		t.Fatal("an unverified PR graduated")
	}
	if got := refresh(forge.PRDraft, true, true); len(got) != 0 {
		t.Fatal("a draft graduated")
	}
	for range 2 {
		got := refresh(forge.PROpen, true, true)
		if len(got) != 1 || got[0].OriginSessionID != sess.ID || got[0].OriginSession.Priority != priority.High {
			t.Fatalf("handoff = %+v; want one linked high-priority review", got)
		}
	}
	if got := refresh(forge.PRDraft, true, true); len(got) != 0 {
		t.Fatal("a PR returned to draft stayed in reviews")
	}
	if got := refresh(forge.PROpen, true, true); len(got) != 1 {
		t.Fatal("a PR made ready again did not return")
	}
	if got := refresh(forge.PRMerged, true, true); len(got) != 0 {
		t.Fatal("a merged PR stayed in reviews")
	}
	if got := refresh(forge.PROpen, true, true); len(got) != 0 {
		t.Fatal("polling reopened a dismissed review")
	}
}

func TestSessionReviewsKeepPriorityAndReturnToTheirConversation(t *testing.T) {
	author := smartSess("author", status.Finished, time.Minute)
	author.Priority = priority.Low
	other := smartSess("other", status.Finished, time.Hour)
	briefing := smartSess("briefing", status.Finished, time.Minute)
	m := smartFleet(t, author, other, briefing)
	m.reviewQueue = true
	m.priorityGroups = map[string]priority.Tier{"release": priority.High}
	m.sessions[0].Group = "release"
	linked := reviewAsk(7, time.Minute)
	linked.OriginSessionID, linked.SessionID = author.ID, briefing.ID
	linked.URL = "https://github.com/example/api/pull/7"
	m.replaceReviews([]store.ReviewRequest{reviewAsk(8, time.Hour), linked})
	m.rebuildRows()
	if m.reviews[0].Number != 7 || m.reviewsTriageKey().priority != priority.High.Rank() || m.tierOf(briefing) != priority.High {
		t.Fatal("the PR and briefing lost the source group's priority")
	}
	if got := reviewDrain(m); !slices.Equal(got, []string{"reviews", "briefing", "other"}) {
		t.Fatalf("drain = %v; want the review in place of the author's completion", got)
	}
	m.triageContent = triageContentReviews
	if !m.inTriageContent(author) {
		t.Fatal("the source was not linked to review work")
	}
	delete(m.priorityGroups, "release")
	m.replaceReviews(m.reviews)
	if m.reviews[0].Number != 8 || m.reviewPriority(linked) != priority.Low || m.tierOf(briefing) != priority.Low {
		t.Fatal("a low-priority source was promoted by the handoff")
	}
	m.reviewsCursor = 1
	detail := ansi.Strip(strings.Join(m.reviewDetailLines(120), "\n"))
	if !strings.Contains(detail, "author") || !strings.Contains(detail, linked.URL) {
		t.Fatalf("missing source or PR link: %s", detail)
	}
	m.reviewsTab = true
	m.reviewsTabAction(keymap.ReviewAuthor)
	selected, ok := m.selected()
	if !ok || selected.ID != author.ID || m.onReviewsTab() {
		t.Fatal("the PR question shortcut did not return to its source")
	}
}
