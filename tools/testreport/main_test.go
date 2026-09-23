package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// events is a test2json stream in the shape `go test -json` writes one.
func events(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

const pkg = "github.com/usestring/gate-inbox/internal/tmux"

func output(test, text string) string {
	return `{"Action":"output","Package":"` + pkg + `","Test":"` + test + `","Output":"` + text + `"}`
}

func action(name, test string) string {
	return `{"Action":"` + name + `","Package":"` + pkg + `","Test":"` + test + `"}`
}

func allowFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "allow.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func report(t *testing.T, stream, allow string) (string, bool) {
	t.Helper()
	results, packageFailures, err := parse(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return render(results, packageFailures, allow)
}

// The whole point of the report is that a skip is visible, so the reason the
// test gave has to survive into the summary rather than a count of skips.
func TestASkipIsReportedWithTheReasonTheTestGave(t *testing.T) {
	stream := events(
		output("TestDrivesARealServer", "=== RUN   TestDrivesARealServer"),
		output("TestDrivesARealServer", "    tmux_test.go:53: tmux not installed"),
		action("skip", "TestDrivesARealServer"),
	)
	text, ok := report(t, stream, "")
	if !ok {
		t.Fatal("a skip with no allowlist is not a failure")
	}
	if !strings.Contains(text, "1 skipped") {
		t.Fatalf("missing the count:\n%s", text)
	}
	if !strings.Contains(text, "tmux not installed") {
		t.Fatalf("missing the reason:\n%s", text)
	}
}

// This is the failure the whole job exists to prevent: tmux disappears, the
// suite skips almost everything, and the exit code still says green.
func TestATestSkippingOutsideTheAllowlistFailsTheRun(t *testing.T) {
	stream := events(
		output("TestDrivesARealServer", "    tmux_test.go:53: tmux not installed"),
		action("skip", "TestDrivesARealServer"),
	)
	text, ok := report(t, stream, allowFile(t, "# nothing is allowed to skip\n"))
	if ok {
		t.Fatalf("an unlisted skip must fail the run:\n%s", text)
	}
	if !strings.Contains(text, "internal/tmux TestDrivesARealServer") {
		t.Fatalf("the offending test must be named:\n%s", text)
	}
}

func TestAnAllowedSkipPasses(t *testing.T) {
	stream := events(
		output("TestOnlyOnDarwin", "    sysstat_test.go:285: darwin only"),
		action("skip", "TestOnlyOnDarwin"),
	)
	allow := allowFile(t, "internal/tmux TestOnlyOnDarwin  # the machine is not a Mac\n")
	if text, ok := report(t, stream, allow); !ok {
		t.Fatalf("a listed skip is expected:\n%s", text)
	}
}

// An allowlist that outlives the reason for it stops describing the run, so a
// test that started passing again has to be taken off it deliberately.
func TestAnAllowlistEntryThatRanIsStale(t *testing.T) {
	stream := events(action("pass", "TestOnlyOnDarwin"))
	allow := allowFile(t, "internal/tmux TestOnlyOnDarwin\n")
	text, ok := report(t, stream, allow)
	if ok {
		t.Fatalf("a stale entry must fail the run:\n%s", text)
	}
	if !strings.Contains(text, "Allowed to skip but ran") {
		t.Fatalf("the report must say why:\n%s", text)
	}
}

// A skip that depends on the host cannot be required, or the run goes red on
// the machines where the thing it waits for did come up.
func TestAConditionalEntryIsNeverStale(t *testing.T) {
	stream := events(action("pass", "TestNeedsAControlClient"))
	allow := allowFile(t, "internal/tmux TestNeedsAControlClient?  # only on hosts where it stalls\n")
	if text, ok := report(t, stream, allow); !ok {
		t.Fatalf("a conditional entry that ran is fine:\n%s", text)
	}
	skipped := events(
		output("TestNeedsAControlClient", "    focusscroll_test.go:51: control client never came up on this host"),
		action("skip", "TestNeedsAControlClient"),
	)
	text, ok := report(t, skipped, allow)
	if !ok {
		t.Fatalf("a conditional entry that skipped is also fine:\n%s", text)
	}
	if !strings.Contains(text, "control client never came up") {
		t.Fatalf("it still has to be reported as uncovered:\n%s", text)
	}
}

// An entry that matches no test allows nothing, so a rename or a typo would
// otherwise quietly stop protecting the test it was written for.
func TestAnAllowlistEntryNamingNoTestFailsTheRun(t *testing.T) {
	stream := events(action("pass", "TestThatExists"))
	allow := allowFile(t, "internal/tmux TestSpeltWrogn?\n")
	text, ok := report(t, stream, allow)
	if ok {
		t.Fatalf("an entry naming nothing must fail the run:\n%s", text)
	}
	if !strings.Contains(text, "TestSpeltWrogn") {
		t.Fatalf("the entry must be named:\n%s", text)
	}
}

// go test dying before it writes anything, or a truncated stream, would
// otherwise be indistinguishable from a suite with nothing to report.
func TestAnEmptyStreamIsAFailure(t *testing.T) {
	text, ok := report(t, "", "")
	if ok {
		t.Fatalf("an empty stream must fail the run:\n%s", text)
	}
	if !strings.Contains(text, "No test results") {
		t.Fatalf("the report must say why:\n%s", text)
	}
}

func TestAFailingTestFailsTheRun(t *testing.T) {
	stream := events(
		output("TestBroken", "    tmux_test.go:12: got 1, want 2"),
		action("fail", "TestBroken"),
	)
	text, ok := report(t, stream, "")
	if ok {
		t.Fatalf("a failure must fail the run:\n%s", text)
	}
	if !strings.Contains(text, "internal/tmux TestBroken") {
		t.Fatalf("the failure must be named:\n%s", text)
	}
}

// A package that will not build reports a failure carrying no test name, so
// counting tests alone would call the run clean.
func TestAPackageThatFailsWithoutATestIsStillAFailure(t *testing.T) {
	stream := events(`{"Action":"fail","Package":"` + pkg + `"}`)
	text, ok := report(t, stream, "")
	if ok {
		t.Fatalf("a package-level failure must fail the run:\n%s", text)
	}
	if !strings.Contains(text, "internal/tmux") {
		t.Fatalf("the package must be named:\n%s", text)
	}
}
