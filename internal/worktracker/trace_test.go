package worktracker

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/tracetest"
)

// The forks are the whole cost of discovery, so each one has to arrive named
// by what it ran and where. A trace that said only "discovery took 400ms"
// would leave the reader exactly where the log already leaves them.
func TestEveryGitForkIsItsOwnSpan(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	spans := tracetest.Capture(t)
	g := newGitCommands()
	g.Branch(dir)
	g.Remote(dir)
	recorded := spans()

	forks := tracetest.Named(recorded, "worktracker.git")
	var subcommands []string
	for _, fork := range forks {
		subcommands = append(subcommands, fork.Attr("git.subcommand").(string))
		if fork.Attr("dir") != dir {
			t.Errorf("fork ran in %v, want the session's directory %s", fork.Attr("dir"), dir)
		}
	}
	// The whole inventory one directory costs, in order, and all of it on the
	// first question: the roots, whether the top level declares submodules, the
	// branch, and the remote. The second question forks nothing.
	if got := strings.Join(subcommands, ","); got != "rev-parse,config,rev-parse,remote" {
		t.Fatalf("subcommands = %s, want rev-parse,config,rev-parse,remote", got)
	}
	// A repository with no origin: the fork failed, and a span that reported
	// it as fine would make the expensive calls indistinguishable from the
	// cheap ones.
	if !forks[len(forks)-1].Failed {
		t.Error("the remote lookup has no origin to find, so its span must carry the failure")
	}
}

// Discovery brackets the forks, and carries nothing the session said. The text
// it scans is the pane and the transcript, and these spans leave the machine.
func TestDiscoveryReportsTheSessionWithoutItsText(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{branch: "alice/abc-133756-slug", remote: "git@github.com:example-org/sample-repo.git"}, f, &now)

	secret := "the operator typed this into the pane"
	spans := tracetest.Capture(t)
	tr.Discover(Session{ID: "sess1", Dir: "/repo", Live: true, Text: secret + " and ABC-133756"})
	recorded := spans()

	span := tracetest.One(t, recorded, "worktracker.discover")
	if span.Attr("session") != "sess1" {
		t.Errorf("session = %v, want sess1", span.Attr("session"))
	}
	if span.Attr("dir") != "/repo" {
		t.Errorf("dir = %v, want /repo", span.Attr("dir"))
	}
	if span.Attr("refs") != int64(1) {
		t.Errorf("refs = %v, want the one reference discovery found", span.Attr("refs"))
	}
	if span.Attr("live") != true {
		t.Errorf("live = %v, want true", span.Attr("live"))
	}
	for key, value := range span.Attrs {
		if text, ok := value.(string); ok && strings.Contains(text, secret) {
			t.Fatalf("attribute %q carries what the session said: %q", key, text)
		}
	}
}

// A refresh is one trace with the two sources under it, because "the pass took
// four seconds" and "GitHub took four seconds of it" are different findings and
// only the second one is actionable.
func TestARefreshCarriesItsSourcesAsChildren(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{
		health:  forge.Health{OK: true},
		prs:     map[string]forge.PR{"example-org/sample-repo#1": {Repo: "example-org/sample-repo", Number: 1}},
		tickets: map[string]forge.Ticket{"ABC-133756": {Identifier: "ABC-133756"}},
	}
	tr := tracker(t, fakeGit{branch: "alice/abc-133756-slug"}, f, &now)
	sessions := []Session{{ID: "sess1", Dir: "/repo", Live: true, Text: "https://github.com/example-org/sample-repo/pull/1"}}
	tr.Discover(sessions[0])

	spans := tracetest.Capture(t)
	tr.Refresh(sessions)
	recorded := spans()

	root := tracetest.One(t, recorded, "worktracker.refresh")
	if root.Parent != "" {
		t.Errorf("the refresh has parent %q; it is the root of its trace", root.Parent)
	}
	if root.Attr("sessions") != int64(1) {
		t.Errorf("sessions = %v, want 1", root.Attr("sessions"))
	}
	if root.Attr("due") != int64(2) {
		t.Errorf("due = %v, want the pull request and the ticket", root.Attr("due"))
	}
	for _, name := range []string{"worktracker.github", "worktracker.linear"} {
		child := tracetest.One(t, recorded, name)
		if child.Parent == "" || child.Trace != root.Trace {
			t.Errorf("%s is not under the refresh: trace %q parent %q", name, child.Trace, child.Parent)
		}
		if child.Attr("resolved") != int64(1) {
			t.Errorf("%s resolved = %v, want 1", name, child.Attr("resolved"))
		}
		if child.Attr("ok") != true {
			t.Errorf("%s ok = %v, want the health the source reported", name, child.Attr("ok"))
		}
	}
}

// A pass with nothing due is still a pass: it forgets dead sessions and flushes
// the sightings, and a trace that dropped it would report a board doing less
// work than it does.
func TestAnEmptyRefreshIsStillRecorded(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := &fakeForge{health: forge.Health{OK: true}}
	tr := tracker(t, fakeGit{}, f, &now)

	spans := tracetest.Capture(t)
	tr.Refresh(nil)
	recorded := spans()

	root := tracetest.One(t, recorded, "worktracker.refresh")
	if root.Attr("due") != int64(0) {
		t.Errorf("due = %v, want 0", root.Attr("due"))
	}
	if len(tracetest.Named(recorded, "worktracker.github")) != 0 {
		t.Error("nothing was due, so no source may appear under the pass")
	}
}
