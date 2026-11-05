package worktracker

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/workspec"
)

func page(t *testing.T, url, title string) workspec.Ref {
	t.Helper()
	ref, ok := workspec.ArtifactRef(url, title)
	if !ok {
		t.Fatalf("%s is not a page address", url)
	}
	return ref
}

// A page the session published is its work the moment it is known: there is no source to ask, so
// it is never handed to GitHub or Linear, never due, and never reads as "not looked up". It stays
// for as long as the session does, and goes with it.
func TestAPublishedPageIsWorkWithoutALookup(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{}, f, &now)
	report := page(t, "https://claude.ai/artifact/Mine1", "Fleet audit")

	session := Session{ID: "s", Text: "mentions https://claude.ai/artifact/Other1", Artifacts: []workspec.Ref{report}, Live: true, Visible: true}
	refs := tr.Discover(session)
	if len(refs) != 1 || refs[0].Key() != report.Key() {
		t.Fatalf("refs = %+v, want only the published page: a mentioned one is not this session's", refs)
	}
	if due := tr.due([]Session{session}); len(due) != 0 {
		t.Errorf("due = %+v, want nothing: a page has no source to ask", due)
	}
	tr.Refresh([]Session{session})
	if f.calls != 0 || f.ticketCalls != 0 {
		t.Errorf("a board on nothing but a page asked GitHub %d and Linear %d times", f.calls, f.ticketCalls)
	}

	work := tr.For("s")
	if !work.Looked[report.Key()] {
		t.Error("the page reads as not looked up, which nothing will ever change")
	}
	tr.MarkSeen([]string{report.Key()})
	now = now.Add(10 * DefaultSettleAfter)
	if tr.For("s").Retired[report.Key()] {
		t.Error("the page was settled off the board while its session is still on it")
	}

	tr.Refresh(nil)
	if refs := tr.For("s").Refs; len(refs) != 0 {
		t.Errorf("the session left the board and its page stayed: %+v", refs)
	}
}

// A page needs no provider, so it is on the board with both sources switched off, and it does not
// count against a source's budget beside the pull requests that do.
func TestAPublishedPageNeedsNoProvider(t *testing.T) {
	tr := New(fakeGit{}, nil, nil)
	report := page(t, "https://artifacts.example.test/a/art_X1", "")
	tr.Discover(Session{ID: "s", Text: "https://github.com/o/r/pull/1", Artifacts: []workspec.Ref{report}})
	refs := tr.For("s").Refs
	if len(refs) != 1 || refs[0].Kind != workspec.KindArtifact {
		t.Errorf("refs = %+v, want the page alone: GitHub is off, the page needs nobody", refs)
	}
}
