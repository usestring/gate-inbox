package worktracker

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/workspec"
)

// Every kind of evidence at once: a branch ticket, a pull request the session opened, a mentioned
// ticket and a mentioned pull request.
const bothKinds = "working on ABC-4242\nhttps://github.com/example-org/sample-repo/pull/7394\nsee PR #12"

func kinds(refs []workspec.Ref) map[workspec.Kind]int {
	out := map[workspec.Kind]int{}
	for _, ref := range refs {
		out[ref.Kind]++
	}
	return out
}

// GitHub off: no pull request is discovered, drawn or asked about, Linear carries on as before,
// and GitHub's health is off rather than failed.
func TestGitHubOffAsksNothingAndDrawsNothing(t *testing.T) {
	now := time.Unix(1700000000, 0)
	linear := &fakeForge{health: forge.Health{OK: true}, tickets: map[string]forge.Ticket{
		"ticket:ABC-4242": {Identifier: "ABC-4242", State: "In Progress", StateType: "started"},
	}}
	tr := New(fakeGit{branch: "abc-100002-slug", remote: "git@github.com:example-org/sample-repo.git"}, nil, linear)
	tr.Now = func() time.Time { return now }

	session := Session{ID: "s", Dir: "/repo", Text: bothKinds, Live: true}
	refs := tr.Discover(session)
	if got := kinds(refs); got[workspec.KindPR] != 0 || got[workspec.KindTicket] == 0 {
		t.Fatalf("discovered %v, want tickets and no pull requests", got)
	}
	tr.Refresh([]Session{session})

	if linear.calls != 0 || linear.ticketCalls != 1 {
		t.Errorf("pull request calls = %d, ticket calls = %d; want 0 and 1", linear.calls, linear.ticketCalls)
	}
	work := tr.For("s")
	if len(work.PRs) != 0 || kinds(work.Refs)[workspec.KindPR] != 0 {
		t.Errorf("work carries pull requests with GitHub off: %+v", work)
	}
	if _, ok := work.Tickets["ticket:ABC-4242"]; !ok {
		t.Errorf("Linear stopped answering when GitHub was switched off: %+v", work.Tickets)
	}
	gh, ln := tr.Health()
	if !gh.Off || gh.OK || gh.Failed() {
		t.Errorf("github health = %+v, want off and not failed", gh)
	}
	if !ln.OK || ln.Off {
		t.Errorf("linear health = %+v, want healthy", ln)
	}
}

// Linear off: no ticket is discovered — including the one a pull request's head branch names —
// and nothing is asked of Linear.
func TestLinearOffAsksNothingAndDrawsNothing(t *testing.T) {
	now := time.Unix(1700000000, 0)
	github := &fakeForge{health: forge.Health{OK: true}, prs: map[string]forge.PR{
		"pr:example-org/sample-repo#7394": {Repo: "example-org/sample-repo", Number: 7394, State: forge.PROpen, HeadRef: "abc-5151-fix"},
	}}
	tr := New(fakeGit{branch: "abc-100002-slug", remote: "git@github.com:example-org/sample-repo.git"}, github, nil)
	tr.Now = func() time.Time { return now }

	session := Session{ID: "s", Dir: "/repo", Text: bothKinds, Live: true}
	if got := kinds(tr.Discover(session)); got[workspec.KindTicket] != 0 || got[workspec.KindPR] == 0 {
		t.Fatalf("discovered %v, want pull requests and no tickets", got)
	}
	tr.Refresh([]Session{session})
	now = now.Add(time.Hour)
	tr.Refresh([]Session{session})

	if github.ticketCalls != 0 || github.calls == 0 {
		t.Errorf("ticket calls = %d, pull request calls = %d; want 0 and some", github.ticketCalls, github.calls)
	}
	work := tr.For("s")
	if len(work.Tickets) != 0 || kinds(work.Refs)[workspec.KindTicket] != 0 {
		t.Errorf("work carries tickets with Linear off: %+v", work.Refs)
	}
	if _, ok := work.PRs["pr:example-org/sample-repo#7394"]; !ok {
		t.Errorf("GitHub stopped answering when Linear was switched off: %+v", work.PRs)
	}
	if _, ln := tr.Health(); !ln.Off || ln.Failed() {
		t.Errorf("linear health = %+v, want off and not failed", ln)
	}
}

// A provider switched off since the last run: its remembered rows are not drawn, the refresh does
// not error or ask about them, and its settled sightings are kept for when it comes back on.
func TestAProviderSwitchedOffLeavesItsRememberedStateAlone(t *testing.T) {
	now := time.Unix(1700000000, 0)
	memory := &fakeMemory{
		prs: []store.StoredPR{{
			Key: "pr:example-org/sample-repo#7394", Repo: "example-org/sample-repo", Number: 7394,
			State: "open", FetchedAt: now.Add(-time.Hour),
		}},
		tickets: []store.StoredTicket{{
			Key: "ticket:ABC-4242", Identifier: "ABC-4242", State: "Done", StateType: "completed",
			FetchedAt: now.Add(-time.Hour),
		}},
	}
	seen := &fakeSeen{seen: map[string]time.Time{"ticket:ABC-4242": now.Add(-time.Hour)}}
	github := &fakeForge{health: forge.Health{OK: true}}
	tr := New(fakeGit{remote: "git@github.com:example-org/sample-repo.git"}, github, nil)
	tr.Now = func() time.Time { return now }
	tr.Memory = memory
	tr.WithSeen(seen)
	if err := tr.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}

	session := Session{ID: "s", Dir: "/repo", Text: bothKinds, Live: true}
	tr.Discover(session)
	tr.Refresh([]Session{session})

	work := tr.For("s")
	if len(work.Tickets) != 0 {
		t.Errorf("a remembered ticket is drawn with Linear off: %+v", work.Tickets)
	}
	if _, ok := work.PRs["pr:example-org/sample-repo#7394"]; !ok {
		t.Errorf("the remembered pull request is gone: %+v", work.PRs)
	}
	if github.ticketCalls != 0 {
		t.Errorf("asked Linear %d times while it is off", github.ticketCalls)
	}
	if len(memory.savedTickets) != 0 {
		t.Errorf("wrote tickets back with Linear off: %+v", memory.savedTickets)
	}
	if _, ok := seen.seen["ticket:ABC-4242"]; !ok {
		t.Error("the ticket's sighting was dropped from the store while Linear is off")
	}
}

type refusingTransport struct{ requests int }

func (r *refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.requests++
	return nil, errors.New("no network in tests")
}

// Linear on with no LINEAR_API_KEY: its tickets are still discovered and kept, with no state, no
// request is sent, and its health says the key is missing rather than that Linear failed.
func TestKeylessLinearKeepsTicketsWithoutState(t *testing.T) {
	now := time.Unix(1700000000, 0)
	transport := &refusingTransport{}
	linear := &forge.Linear{Endpoint: "https://example.invalid", HTTP: &http.Client{Transport: transport}}
	github := &fakeForge{health: forge.Health{OK: true}}
	tr := New(fakeGit{branch: "abc-100002-slug", remote: "git@github.com:example-org/sample-repo.git"}, github, linear)
	tr.Now = func() time.Time { return now }

	session := Session{ID: "s", Dir: "/repo", Text: bothKinds, Live: true}
	if got := kinds(tr.Discover(session)); got[workspec.KindTicket] == 0 || got[workspec.KindPR] == 0 {
		t.Fatalf("discovered %v, want tickets and pull requests", got)
	}
	tr.Refresh([]Session{session})
	now = now.Add(time.Hour)
	tr.Refresh([]Session{session})

	if transport.requests != 0 {
		t.Errorf("sent %d requests to Linear with no key", transport.requests)
	}
	work := tr.For("s")
	if kinds(work.Refs)[workspec.KindTicket] == 0 {
		t.Errorf("ticket references dropped with Linear keyless: %+v", work.Refs)
	}
	if len(work.Tickets) != 0 {
		t.Errorf("tickets carry state with no key: %+v", work.Tickets)
	}
	_, ln := tr.Health()
	if !ln.Off || ln.Failed() || !strings.Contains(ln.Reason, "LINEAR_API_KEY is not set") {
		t.Errorf("linear health = %+v, want off, not failed, naming the missing key", ln)
	}
}
