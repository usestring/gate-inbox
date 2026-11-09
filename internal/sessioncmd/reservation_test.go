// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

func TestReservationPathAliasesConflict(t *testing.T) {
	t.Parallel()
	for _, reverse := range []bool{false, true} {
		name := "relative first"
		if reverse {
			name = "absolute first"
		}
		t.Run(name, func(t *testing.T) {
			h := newSessionHarness(t)
			rival := createReservationCaller(t, h, h.caller.Cwd)
			first := filepath.Join("dir", "file.md")
			second := filepath.Join(h.caller.Cwd, first)
			if reverse {
				first, second = second, first
			}
			if _, err := h.sessions.Reserve(h.caller.ID, []string{first}, "", "", 0); err != nil {
				t.Fatal(err)
			}
			got, err := h.sessions.Reserve(rival, []string{second}, "", "", 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Conflicts) != 1 || got.Conflicts[0].SessionID != h.caller.ID || got.Conflicts[0].Pattern != first {
				t.Errorf("conflicts = %+v, want one lease from %s for %q", got.Conflicts, h.caller.ID, first)
			}
			if len(got.Reserved) != 1 || got.Reserved[0].Pattern != second {
				t.Errorf("reserved = %+v, want the advisory lease for %q", got.Reserved, second)
			}
		})
	}
}

func TestReservationRelativePathsAreScopedToTheirCheckout(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	rival := createReservationCaller(t, h, t.TempDir())
	path := filepath.Join("dir", "file.md")
	if _, err := h.sessions.Reserve(h.caller.ID, []string{path}, "", "", 0); err != nil {
		t.Fatal(err)
	}
	got, err := h.sessions.Reserve(rival, []string{path}, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) != 0 || len(got.Reserved) != 1 {
		t.Fatalf("reservation in separate checkout = %+v, want one lease and no conflicts", got)
	}
}

func TestReservationGlobTreatsTheCheckoutDirectoryLiterally(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	dir := filepath.Join(t.TempDir(), "checkout[demo]")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	holder := createReservationCaller(t, h, dir)
	if _, err := h.sessions.Reserve(holder, []string{"dir/*.md"}, "", "", 0); err != nil {
		t.Fatal(err)
	}
	got, err := h.sessions.Reserve(h.caller.ID, []string{filepath.Join(dir, "dir", "file.md")}, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) != 1 || got.Conflicts[0].Pattern != "dir/*.md" {
		t.Fatalf("conflicts = %+v, want the glob in %q", got.Conflicts, dir)
	}
}

func TestReservationPathAliasRenewsAndReleasesTheSameLease(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	relative := filepath.Join("dir", "file.md")
	absolute := filepath.Join(h.caller.Cwd, relative)
	if _, err := h.sessions.Reserve(h.caller.ID, []string{relative}, "", "first", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.Reserve(h.caller.ID, []string{absolute}, "", "renewed", 0); err != nil {
		t.Fatal(err)
	}
	listed, err := h.sessions.Reservations(h.caller.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Pattern != relative || listed[0].Note != "renewed" {
		t.Fatalf("leases = %+v, want one renewed lease with the original spelling", listed)
	}
	if count, err := h.sessions.ReleaseFiles(h.caller.ID, []string{absolute}); err != nil || count != 1 {
		t.Fatalf("release by absolute path = %d, %v; want one lease released", count, err)
	}

	if _, err := h.sessions.Reserve(h.caller.ID, []string{absolute}, "", "", 0); err != nil {
		t.Fatal(err)
	}
	if count, err := h.sessions.ReleaseFiles(h.caller.ID, []string{relative}); err != nil || count != 1 {
		t.Fatalf("release by relative path = %d, %v; want one lease released", count, err)
	}
}

func createReservationCaller(t *testing.T, h *sessionHarness, dir string) string {
	t.Helper()
	const id = "cafe0001"
	if err := h.store.CreateSession(store.Session{ID: id, Name: "rival", Tool: h.caller.Tool, Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReservationsSurfaceOverlapWithoutBlockingIt(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	rival, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "rival"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	held, err := h.sessions.Reserve(h.caller.ID, []string{"internal/store/*.go"}, "", "adding the inbox table", 0)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(held.Reserved) != 1 || len(held.Conflicts) != 0 {
		t.Fatalf("first reservation = %+v", held)
	}
	if !held.Reserved[0].Mine || held.Reserved[0].Mode != "exclusive" {
		t.Fatalf("reservation = %+v", held.Reserved[0])
	}

	// A literal path under a reserved glob is the collision worktrees miss.
	clash, err := h.sessions.Reserve(rival.ID, []string{"internal/store/store.go"}, "", "", 0)
	if err != nil {
		t.Fatalf("Reserve rival: %v", err)
	}
	if len(clash.Conflicts) != 1 {
		t.Fatalf("overlap was not reported: %+v", clash)
	}
	if clash.Conflicts[0].Holder != "calling-agent" || clash.Conflicts[0].Note != "adding the inbox table" {
		t.Fatalf("conflict = %+v", clash.Conflicts[0])
	}
	// Advisory: the second lease is still granted, so neither agent is stuck.
	if len(clash.Reserved) != 1 {
		t.Fatalf("an advisory lease must still be granted: %+v", clash)
	}

	listed, err := h.sessions.Reservations(h.caller.ID)
	if err != nil {
		t.Fatalf("Reservations: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed = %+v", listed)
	}
}

func TestSharedLeasesOnlyClashWithExclusiveOnes(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	rival, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "rival"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Reserve(h.caller.ID, []string{"docs/usage.md"}, "shared", "", 0); err != nil {
		t.Fatalf("Reserve shared: %v", err)
	}
	quiet, err := h.sessions.Reserve(rival.ID, []string{"docs/usage.md"}, "shared", "", 0)
	if err != nil {
		t.Fatalf("Reserve second shared: %v", err)
	}
	if len(quiet.Conflicts) != 0 {
		t.Fatalf("two shared leases should not clash: %+v", quiet.Conflicts)
	}
	loud, err := h.sessions.Reserve(rival.ID, []string{"docs/usage.md"}, "exclusive", "", 0)
	if err != nil {
		t.Fatalf("Reserve exclusive: %v", err)
	}
	if len(loud.Conflicts) != 1 {
		t.Fatalf("an exclusive lease over a shared one should clash: %+v", loud)
	}
	if _, err := h.sessions.Reserve(h.caller.ID, []string{"docs/usage.md"}, "sideways", "", 0); err == nil ||
		!strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("bad mode error = %v", err)
	}
	if _, err := h.sessions.Reserve(h.caller.ID, nil, "", "", 0); err == nil {
		t.Fatal("reserving nothing should be refused")
	}
	for _, ttl := range []time.Duration{-time.Minute, MaxReservationTTL + time.Minute} {
		if _, err := h.sessions.Reserve(h.caller.ID, []string{"docs/usage.md"}, "", "", ttl); err == nil ||
			!strings.Contains(err.Error(), "outside 0 to "+MaxReservationTTL.String()) {
			t.Fatalf("a %s lease = %v, want a refusal naming the bound", ttl, err)
		}
	}
}

func TestALapsedLeaseStopsBlockingAndReleaseClearsTheRest(t *testing.T) {
	h := newSessionHarness(t)
	rival, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "rival"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A lease whose expiry is already behind us stands in for an agent that
	// died holding one, which is the case the whole advisory design rests on.
	lapsed := time.Now().Add(-time.Minute)
	if _, err := h.store.Reserve([]store.Reservation{{
		ID:         "lapsed01",
		SessionID:  rival.ID,
		Pattern:    "internal/ui/*.go",
		Mode:       store.ReservationExclusive,
		AcquiredAt: lapsed.Add(-time.Hour),
		ExpiresAt:  lapsed,
	}}); err != nil {
		t.Fatalf("write a lapsed lease: %v", err)
	}
	fresh, err := h.sessions.Reserve(h.caller.ID, []string{"internal/ui/model.go"}, "", "", 0)
	if err != nil {
		t.Fatalf("Reserve after lapse: %v", err)
	}
	if len(fresh.Conflicts) != 0 {
		t.Fatalf("a lapsed lease still blocks: %+v", fresh.Conflicts)
	}

	if _, err := h.sessions.Reserve(h.caller.ID, []string{"a.go", "b.go"}, "", "", 0); err != nil {
		t.Fatalf("Reserve more: %v", err)
	}
	released, err := h.sessions.ReleaseFiles(h.caller.ID, []string{"a.go"})
	if err != nil || released != 1 {
		t.Fatalf("ReleaseFiles = %d, %v", released, err)
	}
	released, err = h.sessions.ReleaseFiles(h.caller.ID, nil)
	if err != nil || released != 2 {
		t.Fatalf("ReleaseFiles all = %d, %v", released, err)
	}
	listed, err := h.sessions.Reservations(h.caller.ID)
	if err != nil {
		t.Fatalf("Reservations: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("leases survived a full release: %+v", listed)
	}
}

func TestReleasingBlankPathsDoesNotDropEveryLease(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	if _, err := h.sessions.Reserve(h.caller.ID, []string{"a.go", "b.go"}, "", "", 0); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := h.sessions.ReleaseFiles(h.caller.ID, []string{"  "}); err == nil ||
		!strings.Contains(err.Error(), "omit paths entirely") {
		t.Fatalf("blank release error = %v", err)
	}
	listed, err := h.sessions.Reservations(h.caller.ID)
	if err != nil {
		t.Fatalf("Reservations: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("a blank path released real leases: %+v", listed)
	}
}
