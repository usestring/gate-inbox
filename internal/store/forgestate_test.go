package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

func forgeStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(tmuxtest.ScratchDir(t), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestResolvedStateSurvivesAReopen(t *testing.T) {
	at := time.UnixMilli(1700000000000)
	store := forgeStore(t)

	err := store.SaveForgeState(
		[]StoredPR{{
			Key: "pr:example-org/sample-repo#7674", Repo: "example-org/sample-repo", Number: 7674,
			Title: "Retry truncated status lines", State: "open", Checks: "failing",
			Review: "changes-requested", URL: "https://example.invalid/7674", Mergeable: true,
			HeadRef: "abc-1-fix", FailingChecks: 2, FetchedAt: at,
		}},
		[]StoredTicket{{
			Key: "ticket:ABC-139158", Identifier: "ABC-139158", Title: "Quota",
			State: "In Review", StateType: "started", Assignee: "maintainer",
			URL: "https://example.invalid/ABC-139158", FetchedAt: at,
		}})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	prs, tickets, err := store.ForgeState(at.Add(time.Minute))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(prs) != 1 || len(tickets) != 1 {
		t.Fatalf("loaded %d prs and %d tickets, want 1 and 1", len(prs), len(tickets))
	}
	// Every field, because a column silently dropped in the round trip is a row the board
	// draws wrong rather than one it fails to draw.
	got := prs[0]
	if got.Repo != "example-org/sample-repo" || got.Number != 7674 || got.Title != "Retry truncated status lines" ||
		got.State != "open" || got.Checks != "failing" || got.Review != "changes-requested" ||
		got.URL != "https://example.invalid/7674" || !got.Mergeable || got.HeadRef != "abc-1-fix" ||
		got.FailingChecks != 2 || !got.FetchedAt.Equal(at) {
		t.Errorf("pr round-tripped as %+v", got)
	}
	ticket := tickets[0]
	if ticket.Identifier != "ABC-139158" || ticket.State != "In Review" || ticket.StateType != "started" ||
		ticket.Assignee != "maintainer" || !ticket.FetchedAt.Equal(at) {
		t.Errorf("ticket round-tripped as %+v", ticket)
	}
}

// The reference is the key, not the session: two sessions routinely name the same pull request, and
// one row per reference is what stops the second one overwriting the first with the same answer.
func TestAReferenceIsStoredOnce(t *testing.T) {
	at := time.UnixMilli(1700000000000)
	store := forgeStore(t)

	pr := StoredPR{Key: "pr:example-org/sample-repo#7674", Repo: "example-org/sample-repo", Number: 7674,
		State: "open", FetchedAt: at}
	if err := store.SaveForgeState([]StoredPR{pr}, nil); err != nil {
		t.Fatalf("save: %v", err)
	}
	pr.State = "merged"
	pr.FetchedAt = at.Add(time.Hour)
	if err := store.SaveForgeState([]StoredPR{pr}, nil); err != nil {
		t.Fatalf("resave: %v", err)
	}

	prs, _, err := store.ForgeState(at.Add(2 * time.Hour))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("stored %d rows for one reference, want 1", len(prs))
	}
	if prs[0].State != "merged" {
		t.Errorf("state = %q, want the later answer to win", prs[0].State)
	}
}

// A week-old answer is not worth restoring even marked as stale, and load is the only moment the
// cost of keeping it is being paid.
func TestAnswersOlderThanRetentionAreDroppedOnLoad(t *testing.T) {
	at := time.UnixMilli(1700000000000)
	store := forgeStore(t)

	err := store.SaveForgeState(
		[]StoredPR{
			{Key: "pr:example-org/sample-repo#1", Repo: "example-org/sample-repo", Number: 1, FetchedAt: at},
			{Key: "pr:example-org/sample-repo#2", Repo: "example-org/sample-repo", Number: 2,
				FetchedAt: at.Add(forgeRetention)},
		},
		[]StoredTicket{
			{Key: "ticket:ABC-1", Identifier: "ABC-1", FetchedAt: at},
			{Key: "ticket:ABC-2", Identifier: "ABC-2", FetchedAt: at.Add(forgeRetention)},
		})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// A moment after the older pair has aged out and well before the newer pair has.
	prs, tickets, err := store.ForgeState(at.Add(forgeRetention + time.Minute))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 2 {
		t.Errorf("kept %+v, want only the recent pull request", prs)
	}
	if len(tickets) != 1 || tickets[0].Identifier != "ABC-2" {
		t.Errorf("kept %+v, want only the recent ticket", tickets)
	}

	// Dropped from the table, not merely filtered out of the answer.
	var remaining int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM forge_prs`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 1 {
		t.Errorf("%d rows left in the table, want the aged-out one deleted", remaining)
	}
}

// Nothing to write must not open a transaction: a refresh that resolved nothing is the common case
// on a quiet board, and it runs every tick.
func TestSavingNothingIsANoOp(t *testing.T) {
	store := forgeStore(t)
	if err := store.SaveForgeState(nil, nil); err != nil {
		t.Errorf("save: %v", err)
	}
}
