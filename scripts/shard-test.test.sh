#!/usr/bin/env bash
# Tests for shard-test.sh, against a generated Go package rather than
# internal/ui, so the run is a second rather than a minute.
#
# The property worth testing is the one a reader cannot check by eye: that
# splitting the tests across processes runs every test exactly once. Each
# generated test appends its own name to a shared file, so a dropped test and a
# double-run test are both visible in the tally afterwards.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
runner="$script_dir/shard-test.sh"

test_root="$(mktemp -d "${TMPDIR:-/tmp}/shard-test.XXXXXX")"
trap 'rm -rf "$test_root"' EXIT

failures=0
# Reports every failing case rather than exiting on the first, matching the
# other *.test.sh here: one broken case otherwise hides the rest for a whole
# debug cycle.
fail() {
	echo "FAIL: $*" >&2
	failures=$((failures + 1))
}

module="$test_root/mod"
mkdir -p "$module/pkg"
cat >"$module/go.mod" <<'GOMOD'
module shardfixture

go 1.26
GOMOD

# TestSlow keeps the last shard running after the others are done, so a deal
# that loses a name cannot be masked by everything finishing at once.
# TestCanFail fails only on request, so one fixture covers both paths.
cat >"$module/pkg/pkg_test.go" <<'GO'
package pkg

import (
	"os"
	"testing"
	"time"
)

func record(t *testing.T) {
	t.Helper()
	f, err := os.OpenFile(os.Getenv("SHARD_TALLY"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(t.Name() + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestCase0(t *testing.T) { record(t) }
func TestCase1(t *testing.T) { record(t) }
func TestCase2(t *testing.T) { record(t) }
func TestCase3(t *testing.T) { record(t) }
func TestCase4(t *testing.T) { record(t) }
func TestCase5(t *testing.T) { record(t) }
func TestCase6(t *testing.T) { record(t) }
func TestCase7(t *testing.T) { record(t) }
func TestCase8(t *testing.T) { record(t) }

func TestSlow(t *testing.T) { record(t); time.Sleep(200 * time.Millisecond) }

// Skips before recording, so -test.short is visible in the tally as well as in
// the runner's own skip count.
func TestShortSkipped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped under -short")
	}
	record(t)
}

func TestCanFail(t *testing.T) {
	record(t)
	if os.Getenv("SHARD_FIXTURE_FAIL") != "" {
		t.Fatal("failing on request")
	}
}
GO

readonly fixture_tests=12
tally="$test_root/tally"
export SHARD_TALLY="$tally"

run_in_module() { (cd "$module" && "$runner" "$@"); }

# 1. Every test runs exactly once. Twenty shards is more shards than tests, so
#    an off-by-one in the deal shows up as an empty shard or a dropped name.
for shards in 4 20; do
	: >"$tally"
	run_in_module --package ./pkg --shards "$shards" >"$test_root/out.$shards" 2>&1 ||
		fail "$shards-shard run exited non-zero: $(cat "$test_root/out.$shards")"
	grep -q '^ok$' "$test_root/out.$shards" ||
		fail "$shards-shard run did not report ok: $(cat "$test_root/out.$shards")"
	[ "$(wc -l <"$tally")" -eq "$fixture_tests" ] ||
		fail "$shards shards ran $(wc -l <"$tally") tests, want $fixture_tests"
	[ "$(sort -u "$tally" | wc -l)" -eq "$fixture_tests" ] ||
		fail "$shards shards ran a test twice: $(sort "$tally" | uniq -d | tr '\n' ' ')"
	# The default run probes with -test.short and then runs whatever skipped
	# again, alone. TestShortSkipped is the fixture's stand-in for a test
	# that needs a quiet box, so the phase must appear and must cover it --
	# the full tally above is what proves it actually ran.
	grep -q '^  quiet   1 tests' "$test_root/out.$shards" ||
		fail "$shards shards: no quiet phase for the -short-skipped test: $(cat "$test_root/out.$shards")"
done

# 2. --serial pulls a test out of the shards, and it still runs exactly once.
: >"$tally"
run_in_module --package ./pkg --shards 4 --serial '^TestSlow$' >"$test_root/out.serial" 2>&1 ||
	fail "serial run exited non-zero: $(cat "$test_root/out.serial")"
grep -q '1 serial' "$test_root/out.serial" ||
	fail "--serial did not select exactly one test: $(cat "$test_root/out.serial")"
[ "$(wc -l <"$tally")" -eq "$fixture_tests" ] ||
	fail "serial run ran $(wc -l <"$tally") tests, want $fixture_tests"
[ "$(sort -u "$tally" | wc -l)" -eq "$fixture_tests" ] ||
	fail "serial run ran a test twice: $(sort "$tally" | uniq -d | tr '\n' ' ')"

# 3. A --serial pattern matching nothing warns rather than passing quietly,
#    which is what catches a test being renamed out from under it.
run_in_module --package ./pkg --shards 4 --serial '^TestNoSuchThing$' >"$test_root/out.nomatch" 2>&1 ||
	fail "run with an unmatched --serial exited non-zero"
grep -q 'matched no test' "$test_root/out.nomatch" ||
	fail "unmatched --serial did not warn: $(cat "$test_root/out.nomatch")"

# 4. A failing test fails the run and is named. A runner that swallows a
#    failure is worse than no runner.
SHARD_FIXTURE_FAIL=1 run_in_module --package ./pkg --shards 4 >"$test_root/out.fail" 2>&1 &&
	fail "run with a failing test exited zero"
grep -q 'TestCanFail' "$test_root/out.fail" ||
	fail "failing test was not named: $(cat "$test_root/out.fail")"

# 5. Flags after -- reach the test binary. -test.short is observable: the
#    fixture has one test that skips under it, so both the tally and the
#    runner's skip count should move.
: >"$tally"
run_in_module --package ./pkg --shards 4 -- -test.short >"$test_root/out.short" 2>&1 ||
	fail "short run exited non-zero: $(cat "$test_root/out.short")"
[ "$(wc -l <"$tally")" -eq "$((fixture_tests - 1))" ] ||
	fail "-- -test.short did not reach the binary; tally has $(wc -l <"$tally")"
grep -q 'note: 1 of' "$test_root/out.short" ||
	fail "skip was not counted: $(cat "$test_root/out.short")"

# 6. A passthrough -test.run is refused. Go takes the last -test.run on the
#    line, so it would replace each shard's own selection and every shard
#    would run the same subset -- the partition gone, silently.
run_in_module --package ./pkg --shards 4 -- -test.run '^TestCase0$' >"$test_root/out.runflag" 2>&1 &&
	fail "passthrough -test.run was accepted; it silently breaks the partition"
grep -q "replaces each shard's own selection" "$test_root/out.runflag" ||
	fail "refusal did not explain itself: $(cat "$test_root/out.runflag")"

# 7. A package with no tests is not an error.
mkdir -p "$module/empty"
echo 'package empty' >"$module/empty/empty.go"
run_in_module --package ./empty >"$test_root/out.empty" 2>&1 ||
	fail "empty package exited non-zero: $(cat "$test_root/out.empty")"

[ "$failures" -eq 0 ] || {
	echo "$failures case(s) failed" >&2
	exit 1
}
echo "ok"
