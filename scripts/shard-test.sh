#!/usr/bin/env bash
# Run one Go package's tests across several processes.
#
# internal/ui is ~1460 tests with no t.Parallel, and its cost is latency rather
# than computation: a serial run sits at about half a core, blocked on tmux
# round trips. Splitting it across processes overlaps that waiting.
#
# Processes rather than goroutines because the package's render memo, theme and
# glyph set are package-level variables that New() rewrites for every Model, so
# tests running concurrently inside one process race on them -- measured at 276
# data races from making 61 tests parallel. Separate processes each get their
# own copy.
set -euo pipefail
shopt -s nullglob

pkg=./internal/ui
# One shard per core. The bulk phase is latency rather than compute, so more
# shards do keep shrinking it -- measured on an 8-core box, 1461 tests: 29s at
# 8 shards, 26s at 16, 29s at 24 and at 32 -- but the serial phase below is
# 37s of wall clock either way, so the whole run only moves from 66s to 63s.
# Three seconds is not worth what the extra concurrency costs: at 16 shards
# and load 29, TestSeveralDeliveriesInOnePassStayOffIt went over its 400ms
# delivery budget and failed, twice. Tests that assert a latency are not
# confined to the serial list below, so the safe default is the one that has
# not been seen to break them.
shards=$(nproc 2>/dev/null || echo 4)
# Tests to run alone, before the shards, for a package that needs it. None by
# default: a package's own tests are the right place to say they need a quiet
# box, which is what testing.Short() does in internal/ui. README.md has the
# reasoning.
serial=''

usage() {
	cat >&2 <<'USAGE'
usage: scripts/shard-test.sh [--package P] [--shards N] [--serial REGEX] [-- GO_TEST_FLAGS...]

  --package P   package to test (default ./internal/ui)
  --shards N    concurrent processes (default: nproc)
  --serial R    tests matching R run alone first (default: none)

Flags after -- go to the test binary, e.g. -- -test.short
USAGE
	exit 2
}

extra=()
while [ $# -gt 0 ]; do
	case "$1" in
	--package) pkg=${2:?--package needs a value}; shift 2 ;;
	--shards) shards=${2:?--shards needs a value}; shift 2 ;;
	--serial) [ $# -ge 2 ] || { echo "--serial needs a value ('' to disable)" >&2; exit 2; }
		serial=$2; shift 2 ;;
	--) shift; extra=("$@"); break ;;
	-h | --help) usage ;;
	*) echo "unknown argument: $1" >&2; usage ;;
	esac
done
case "$shards" in '' | *[!0-9]* | 0) echo "--shards must be a positive integer, got: $shards" >&2; exit 2 ;; esac
# Go takes the last -test.run on the command line, and each shard's selection
# is passed there, so a passthrough one silently replaces it: every shard then
# runs the same subset and the partition is gone. Refuse rather than sharding
# something other than what was asked for.
user_short=0
for flag in ${extra[@]+"${extra[@]}"}; do
	case "$flag" in
	-test.short | -test.short=* | -short | -short=*) user_short=1 ;;
	-test.run | -test.run=* | -run | -run=*)
		echo "cannot pass $flag through: it replaces each shard's own selection" >&2
		echo "use --serial, or run that subset with plain go test" >&2
		exit 2
		;;
	esac
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

bin=$work/pkg.test
CGO_ENABLED=${CGO_ENABLED:-0} go test -c -o "$bin" "$pkg"
# `go test` runs a package's tests with the package directory as the working
# directory, and a precompiled binary does not. Every testdata/... path in the
# package resolves against it, so running the shards from anywhere else fails
# them all on a missing fixture that is really there.
pkgdir=$(go list -f '{{.Dir}}' "$pkg")
# A package with no test files compiles to nothing rather than to an empty binary.
[ -x "$bin" ] || { echo "no test binary for $pkg (no test files?)" >&2; exit 0; }

"$bin" -test.list '.*' | grep '^Test' | sort > "$work/all"
total=$(wc -l < "$work/all")
[ "$total" -gt 0 ] || { echo "no tests in $pkg" >&2; exit 0; }

awk -v re="$serial" -v s="$work/serial" -v b="$work/bulk" \
	'{ print > ((re != "" && $0 ~ re) ? s : b) }' "$work/all"
touch "$work/serial" "$work/bulk"
nserial=$(wc -l < "$work/serial")
nbulk=$(wc -l < "$work/bulk")
# A renamed or added budget test would otherwise fall into the shard pool in
# silence, and surface later as somebody else's flaky rate assertion.
[ -z "$serial" ] || [ "$nserial" -gt 0 ] || echo "warning: --serial matched no test in $pkg" >&2
echo "$pkg: $total tests, $nserial serial, $nbulk across $shards shards"

# $TMUX overrides TMUX_TMPDIR, so a run started from inside tmux puts every
# shard's fixtures on the operator's live server -- documented in
# docs/upstream/AGENTS.md as the reason a bare `go test` is never right here.
# Each run gets its own tmux root, which also takes the sockets with it on exit
# rather than leaving them in the shared directory.
mkdir -p "$work/tmux"
run() { # run <namefile> [extra flag...]: writes <namefile>.out
	local names=$1
	shift
	[ -s "$names" ] || return 0
	# A subshell cd rather than `env -C`, which BSD env (macOS) does not have.
	( cd "$pkgdir" && env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$work/tmux" \
		"$bin" -test.run "^($(paste -sd'|' "$names"))\$" -test.count=1 -test.v \
		"$@" ${extra[@]+"${extra[@]}"} ) > "$names.out" 2>&1
}

# Dealt round-robin, not contiguous: neighbouring test names are usually the
# same file, so contiguous blocks put a slow file's tests all in one shard.
# Measured on internal/ui at 8 shards: 55s dealt against 65-69s contiguous.
awk -v k="$shards" -v out="$work/shard" '{ print > (out "." (NR % k)) }' "$work/bulk"

# A runner that quietly runs 1400 of 1468 tests and says ok is worse than no
# runner. The partition is by line rather than by -run regex ranges, so it
# cannot drop or double a test by construction -- but "by construction" is the
# claim every silently-wrong partition also makes, and it costs four lines to
# stop claiming it and check. `<` is a test that would not have run, `>` one
# that would have run twice or that is not in the list at all.
#
# Before anything runs, so a bad partition costs no time rather than a serial
# phase first.
sort "$work/serial" "$work"/shard.[0-9]* > "$work/covered"
if ! cmp -s "$work/all" "$work/covered"; then
	echo "shard partition does not cover the test list:" >&2
	diff "$work/all" "$work/covered" >&2 || true
	exit 1
fi

failed=0
start=$SECONDS
if [ "$nserial" -gt 0 ]; then
	phase=$SECONDS
	run "$work/serial" || failed=1
	echo "  serial  $nserial tests  $((SECONDS - phase))s"
fi

# The shards run -test.short unless the caller named their own serial set or
# asked for -short themselves. A test that needs a quiet box says so with
# testing.Short(), so this is how the runner learns which tests those are
# without carrying their names: they skip here, and the phase below runs
# exactly those again, alone and without -short.
#
# It matters. A fork-budget test sharded alongside everything else failed on
# this box at load 57 -- they assert rates against floors as well as ceilings,
# and sharding them buys nothing because they are pure wall clock.
short_probe=()
if [ "$nserial" -eq 0 ] && [ "$user_short" -eq 0 ]; then
	short_probe=(-test.short)
fi

phase=$SECONDS
pids=()
for chunk in "$work"/shard.*; do
	run "$chunk" ${short_probe[@]+"${short_probe[@]}"} & pids+=($!)
done
for pid in ${pids[@]+"${pids[@]}"}; do wait "$pid" || failed=1; done
echo "  shards  $nbulk tests  $((SECONDS - phase))s"

if [ "${#short_probe[@]}" -gt 0 ]; then
	# Whatever skipped under -short, run alone and in full. Names come from
	# the tests themselves rather than from a list kept here.
	awk '/^[[:space:]]*--- SKIP/ { sub(/^[[:space:]]*--- SKIP: /, ""); sub(/ \(.*/, "");
	     if (index($0, "/") == 0) print }' "$work"/shard.*.out | sort -u > "$work/quiet"
	nquiet=$(wc -l < "$work/quiet")
	if [ "$nquiet" -gt 0 ]; then
		phase=$SECONDS
		run "$work/quiet" || failed=1
		echo "  quiet   $nquiet tests  $((SECONDS - phase))s"
	fi
fi
echo "total     $((SECONDS - start))s"

# A shard that skipped everything -- no tmux, say -- exits 0 with no FAIL line,
# so counting skips is what keeps a green run from being a vacuous one. This is
# also why the shards run -test.v: without it Go prints nothing at all for a
# passing or a skipped test, and this count is silently always zero.
# tools/testreport reads a test2json stream and reports each skip with the
# reason the test gave, which is better than this count -- but it takes
# ci-allowed-skips.txt as policy, failing when an allowlisted test runs and
# when a skip is not listed. That is a whole-suite CI contract: the
# fork-budget tests are not in it, so it would fail the `-- -test.short`
# invocation this runner recommends, and any single-package run. A count is
# the honest thing to report from a partial run.
# When the quiet phase ran, the shards' own skips are the -short probe rather
# than a real result: every one of them was run again there. Counting those
# files would report the probe as missing coverage.
tally=("$work"/*.out)
if [ "${#short_probe[@]}" -gt 0 ]; then
	# Both phases are conditional, and these are literal paths rather than
	# globs, so nullglob will not drop a missing one: test for each.
	tally=()
	[ -f "$work/serial.out" ] && tally+=("$work/serial.out")
	[ -f "$work/quiet.out" ] && tally+=("$work/quiet.out")
fi
skipped=$(awk '/^[[:space:]]*--- SKIP/ && index($0, "/") == 0 { s++ }
	END { print s + 0 }' ${tally[@]+"${tally[@]}"})
# Against the package's whole test count, because that is the number the
# reader is deciding about: how much of the suite actually reported.
[ "$skipped" -eq 0 ] || echo "note: $skipped of $total skipped"

if [ "$failed" -ne 0 ]; then
	echo
	echo "failures:"
	# Top-level names first, as a list to scan; the detail follows. `\s` is
	# a GNU extension, so this anchors at column zero instead, which is also
	# what keeps subtests out of the summary.
	#
	# -a is load-bearing. A tmux pane capture can end mid-character, so the
	# output holds a truncated multi-byte sequence; grep then calls the file
	# binary and reports no match at all, printing an empty failure list for
	# a run that failed. Observed on a real run here, where the summary went
	# blank while awk below still found the failure.
	grep -ahE '^--- FAIL' "$work"/*.out | sort -u
	echo
	# The names alone are not enough to act on, and the run took long enough
	# that re-running to see why is a real cost.
	# -test.v streams a test's log lines as they happen, so they arrive
	# BEFORE its result line, not indented beneath it. A window that starts
	# at "--- FAIL" therefore shows the next test's output and drops the
	# reason this one failed. Buffer each top-level test instead and print
	# the buffer once its result turns out to be a failure. A subtest's
	# "=== RUN" carries a slash, which is what distinguishes it from the
	# parent whose buffer it belongs in.
	awk 'function flush() { if (bad) printf "%s", buf; buf = ""; bad = 0 }
	     FNR == 1 { flush() }
	     /^=== RUN/ && index($0, "/") == 0 { flush() }
	     { buf = buf $0 "\n" }
	     /^--- FAIL/ { bad = 1 }
	     END { flush() }' "$work"/*.out | cut -c1-200 | head -n 80
	exit 1
fi
echo "ok"
