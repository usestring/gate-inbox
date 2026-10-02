#!/usr/bin/env bash
# Run the test suite the way CI runs it, so green here means green there.
#
# CI calls this script too (.github/workflows/ci.yml), so the two cannot drift
# apart. It pins what a developer's shell would otherwise decide:
#
#   - a UTF-8 locale: panes draw box-drawing characters and a prompt glyph
#     through tmux, under the C locale tmux renders them as replacement
#     characters, and the tests waiting for them time out;
#   - TERM, which the tests' tmux servers inherit;
#   - CGO_ENABLED=0, as the release build has it;
#   - -count=1, so a cached pass is never reported as a run.
#
# go test runs under scripts/test-tmux-isolation.sh, which fails the run if
# anything reached the tmux server it inherits. The stream then goes through
# tools/testreport against ci-allowed-skips.txt: a test that skips without
# being listed fails the run just as a failing test does, because a suite that
# skipped its way to green did not run.
set -euo pipefail

usage() {
	cat >&2 <<'USAGE'
usage: scripts/ci-test.sh [--stream FILE] [--no-report] [PACKAGE...]
       scripts/ci-test.sh --report STREAM...

  PACKAGE...     packages to test (default ./...)
  --stream FILE  keep the go test -json stream in FILE
  --no-report    skip the allowlist check, for a run of part of the suite
  --report       check streams other runs wrote, and run no tests

TESTREPORT names a prebuilt tools/testreport binary; without it one is built.
USAGE
	exit 2
}

stream=''
report=1
report_only=0
while [ $# -gt 0 ]; do
	case "$1" in
	--stream) stream=${2:?--stream needs a file}; shift 2 ;;
	--no-report) report=0; shift ;;
	--report) report_only=1; shift; break ;;
	-h | --help) usage ;;
	--) shift; break ;;
	-*) echo "unknown argument: $1" >&2; usage ;;
	*) break ;;
	esac
done

export LANG=C.UTF-8 LC_ALL=C.UTF-8 TERM=xterm-256color CGO_ENABLED=0
cd "$(dirname "$0")/.."

work=$(mktemp -d "${TMPDIR:-/tmp}/ci-test.XXXX")
trap 'rm -rf "$work"' EXIT

# check STREAM...: the allowlist verdict over every stream at once. The
# allowlist is a whole-suite contract, so a stream that covers part of the
# suite reads every listed test the other parts ran as never seen.
check() {
	local bin=${TESTREPORT:-}
	if [ -z "$bin" ]; then
		bin=$work/testreport
		go build -o "$bin" ./tools/testreport
	fi
	local summary=()
	[ -z "${GITHUB_STEP_SUMMARY:-}" ] || summary=(-summary "$GITHUB_STEP_SUMMARY")
	cat "$@" | "$bin" -allow ci-allowed-skips.txt ${summary[@]+"${summary[@]}"}
}

if [ "$report_only" -eq 1 ]; then
	[ $# -gt 0 ] || usage
	check "$@"
	exit
fi

[ $# -gt 0 ] || set -- ./...
[ -n "$stream" ] || stream=$work/test.json

suite=0
scripts/test-tmux-isolation.sh -- \
	go test -json -count=1 -timeout 15m "$@" > "$stream" || suite=$?

# Both verdicts have to be clean: go test alone would call a suite that
# passed by skipping a success, and testreport alone would miss a failure
# that never reached the stream.
verdict=0
[ "$report" -eq 0 ] || check "$stream" || verdict=$?
[ "$suite" -eq 0 ] && [ "$verdict" -eq 0 ]
