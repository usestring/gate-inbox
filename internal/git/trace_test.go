package git

import (
	"os/exec"
	"testing"

	"github.com/usestring/gate-inbox/internal/tracetest"
)

// This driver's only cost is the processes it forks, so a span per fork is the
// measurement, and it has to say which subcommand ran: a rev-parse walking out
// of nested submodules and a toplevel lookup are the same function call and
// nothing like the same cost.
func TestEachForkIsNamedBySubcommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	driver, err := New()
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	spans := tracetest.Capture(t)
	if _, err := driver.RepoRoot(dir); err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "git.run")
	if span.Attr("git.subcommand") != "rev-parse" {
		t.Errorf("git.subcommand = %v, want rev-parse", span.Attr("git.subcommand"))
	}
	if span.Attr("dir") != dir {
		t.Errorf("dir = %v, want %s", span.Attr("dir"), dir)
	}
	if span.Failed {
		t.Error("the call succeeded, so its span must not carry a failure")
	}
}

// A fork that failed has to say so. The whole use of these spans is telling a
// slow answer from no answer, and a directory that is not a repository is the
// common no-answer on a board full of scratch directories.
func TestAFailedForkCarriesTheFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	driver, err := New()
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	spans := tracetest.Capture(t)
	if _, err := driver.RepoRoot(t.TempDir()); err == nil {
		t.Fatal("a bare temp directory is not a repository; RepoRoot must refuse it")
	}
	recorded := spans()

	span := tracetest.One(t, recorded, "git.run")
	if !span.Failed {
		t.Error("the fork failed, so its span must carry the failure")
	}
}

// The arguments past the subcommand are paths and refs. They say nothing more
// about what the call cost, and a span is shipped off this machine.
func TestOnlyTheSubcommandIsRecorded(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	driver, err := New()
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	spans := tracetest.Capture(t)
	driver.IsRepoRoot(dir)
	recorded := spans()

	span := tracetest.One(t, recorded, "git.run")
	if span.Attr("git.subcommand") != "rev-parse" {
		t.Fatalf("git.subcommand = %v, want rev-parse alone", span.Attr("git.subcommand"))
	}
	for key, value := range span.Attrs {
		if text, ok := value.(string); ok && text == "--show-toplevel" {
			t.Fatalf("attribute %q carries the flag as well as the subcommand", key)
		}
	}
}
