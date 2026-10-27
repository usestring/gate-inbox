package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/store"
)

// A thread names its line by one side's number, and a kept line carries the
// same number on both sides only by coincidence: a LEFT thread on old line 2
// belongs under the removed line, never under the kept line that is new
// line 2.
func TestFilesTabDrawsEachThreadUnderItsLine(t *testing.T) {
	thread := func(side string, line int, body string) forge.ReviewThread {
		return forge.ReviewThread{Path: "a.go", Side: side, Line: line, Comments: []forge.PRComment{{Author: "bob", Body: body}}}
	}
	detail := &forge.PRDetail{
		Files: []forge.PRFile{{Path: "a.go", Patch: "@@ -1,3 +1,3 @@\n keep\n-gone\n+came\n same\n"}},
		Threads: []forge.ReviewThread{
			thread("LEFT", 2, "why remove"),
			thread("RIGHT", 2, "nice add"),
			thread("RIGHT", 3, "on the kept line"),
			{Path: "a.go", Line: 0, Outdated: true, Comments: []forge.PRComment{{Author: "bob", Body: "old"}}},
		},
	}
	lines := filesLines(detail, nil, 0, 100).lines
	var text []string
	for _, line := range lines {
		var b strings.Builder
		for _, span := range line {
			b.WriteString(span.Text)
		}
		text = append(text, strings.TrimSpace(b.String()))
	}
	after := func(code, body string) {
		t.Helper()
		for i, line := range text {
			if strings.HasSuffix(line, code) {
				for _, below := range text[i+1:] {
					if strings.HasPrefix(below, "┃") {
						if strings.Contains(below, body) {
							return
						}
						continue
					}
					break
				}
				t.Errorf("%q is not drawn under %q:\n%s", body, code, strings.Join(text, "\n"))
				return
			}
		}
		t.Errorf("no line %q:\n%s", code, strings.Join(text, "\n"))
	}
	after("-gone", "why remove")
	after("+came", "nice add")
	after(" same", "on the kept line")
	if !strings.Contains(strings.Join(text, "\n"), "Outdated conversation") {
		t.Errorf("the outdated thread was dropped:\n%s", strings.Join(text, "\n"))
	}
}

// A review carries only what the operator kept, and only where GitHub will
// take it: a kept draft whose line left the diff would fail the whole review.
func TestSubmittedReviewCarriesKeptDraftsOnTheDiff(t *testing.T) {
	detail := &forge.PRDetail{HeadSHA: "current", Files: []forge.PRFile{{Path: "a.go", Patch: "@@ -1,3 +1,3 @@\n keep\n-gone\n+came\n same\n"}}}
	draft := func(side string, line int, state, body string) store.ReviewDraft {
		return store.ReviewDraft{HeadSHA: "current", Path: "a.go", Side: side, Line: line, State: state, Body: body}
	}
	threads, left := reviewThreads(detail, []store.ReviewDraft{
		draft("LEFT", 2, store.DraftKept, "on the removed line"),
		draft("RIGHT", 2, store.DraftProposed, "never kept"),
		draft("RIGHT", 3, store.DraftDropped, "dropped"),
		draft("RIGHT", 40, store.DraftKept, "off the diff"),
		draft("RIGHT", 2, store.DraftKept, "on the added line"),
		{HeadSHA: "old", Path: "a.go", Side: "RIGHT", Line: 2, State: store.DraftKept, Body: "on replacement code"},
	})
	want := []forge.DraftThread{
		{Path: "a.go", Side: "LEFT", Line: 2, Body: "on the removed line"},
		{Path: "a.go", Side: "RIGHT", Line: 2, Body: "on the added line"},
	}
	if len(threads) != len(want) || threads[0] != want[0] || threads[1] != want[1] || left != 2 {
		t.Fatalf("threads %+v, %d left out; want %+v, 2 left out", threads, left, want)
	}
}

func TestReviewSubmissionStaysPendingAcrossPageNavigation(t *testing.T) {
	m := &Model{}
	view := m.reviewsPR()
	r := store.ReviewRequest{Repo: "o/r", Number: 7}
	view.page = &prPage{review: r}
	view.cache.details = map[string]*forge.PRDetail{view.page.key(): {NodeID: "PR_1", HeadSHA: "abc"}}
	if cmd := m.submitReview(view.page, forge.ReviewApprove, ""); cmd == nil {
		t.Fatal("first submission was refused")
	}
	view.page = &prPage{review: r}
	m.composeReview(forge.ReviewApprove)
	if view.page.compose != nil {
		t.Fatal("another summary opened while the review was pending")
	}
	if cmd := m.submitReview(view.page, forge.ReviewApprove, ""); cmd != nil {
		t.Fatal("a second review could be submitted while the first was pending")
	}
	m.finishSubmit(reviewSubmittedMsg{review: r, err: errors.New("refused")})
	if cmd := m.submitReview(view.page, forge.ReviewApprove, ""); cmd == nil {
		t.Fatal("a failed submission could not be retried")
	}
}

func TestReviewComposePastePreservesMultilineBody(t *testing.T) {
	m := &Model{reviewQueue: true, reviewsTab: true}
	view := m.reviewsPR()
	view.page = &prPage{compose: &draftCompose{text: []rune("prefix ")}}
	m.update(tea.PasteMsg{Content: "first\r\nsecond\rthird\x00 café"})
	if got := string(view.page.compose.text); got != "prefix first\nsecond\nthird café" {
		t.Fatalf("pasted body = %q", got)
	}
}
