package sessioncmd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

func TestReplacedReviewSessionCannotDraftAgainstTheNewHead(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	ask := store.ReviewRequest{Repo: "example/api", Number: 7, Trigger: "requested", TriggerID: "R_1", HeadSHA: "aaaaaaa"}
	ingest := func() {
		t.Helper()
		if _, err := st.UpsertReviewRequests([]store.ReviewRequest{ask}, now); err != nil {
			t.Fatal(err)
		}
	}
	attach := func(id string) {
		t.Helper()
		if err := st.AttachReviewSession(ask.Repo, ask.Number, id, ask.HeadSHA, now); err != nil {
			t.Fatal(err)
		}
	}
	ingest()
	attach("a1")

	ask.HeadSHA = "bbbbbbb"
	ingest()
	in := ReviewCommentInput{Repo: ask.Repo, Number: ask.Number, Path: "main.go", Line: 3, Body: "finding from A"}
	if _, err := ReviewComment(dir, "a1", in, now); err != nil {
		t.Fatal(err)
	}
	attach("b2")
	if _, err := ReviewComment(dir, "a1", in, now); err == nil || !strings.Contains(err.Error(), "review session") {
		t.Fatalf("old session should be refused with an actionable error, got %v", err)
	}
	in.Body = "finding from B"
	if _, err := ReviewComment(dir, "b2", in, now); err != nil {
		t.Fatal(err)
	}
	drafts, err := st.ReviewDrafts(ask.Repo, ask.Number)
	if err != nil || len(drafts) != 2 || drafts[0].HeadSHA != "aaaaaaa" || drafts[1].HeadSHA != "bbbbbbb" {
		t.Fatalf("drafts = %+v, err = %v; want the original A draft and the new B draft", drafts, err)
	}
}
