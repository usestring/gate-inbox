#!/usr/bin/env bash
# Prove the test suite cannot touch a live tmux server, whatever environment
# it inherits.
#
# A test cleanup that runs a bare `tmux kill-server` with TMUX inherited from
# the caller's pane kills the caller's live server, because tmux honours TMUX
# ahead of TMUX_TMPDIR. This script recreates
# that environment around a sentinel server nobody else uses:
#
#   1. start a sentinel server on a private socket, with one session;
#   2. export TMUX and TMUX_PANE pointing at it -- the dangerous inherited
#      value -- and unset nothing;
#   3. run the suite;
#   4. require the sentinel server to be alive with exactly the sessions,
#      windows and panes it had before.
#
# Usage:
#   scripts/test-tmux-isolation.sh                 # go test -count=1 ./...
#   scripts/test-tmux-isolation.sh -- CMD [ARGS]   # any command instead
#
# The command's stdout is passed through untouched (CI pipes go test -json
# from it); the sentinel report goes to stderr. Exit status: the command's own
# if the sentinel survived untouched, 3 if it did not.
set -euo pipefail

if ! command -v tmux >/dev/null 2>&1; then
	echo "test-tmux-isolation: tmux is not installed" >&2
	exit 2
fi

if [ "${1:-}" = "--" ]; then
	shift
fi
if [ $# -eq 0 ]; then
	set -- go test -count=1 ./...
fi

# Short on purpose: a tmux socket path is capped near 104 bytes.
work=$(mktemp -d "${TMPDIR:-/tmp}/gisent.XXXX")
sock="$work/sentinel.sock"

# Every tmux call here names the sentinel by -S, so none of them can follow
# an inherited TMUX anywhere else.
sentinel() {
	env -u TMUX -u TMUX_PANE tmux -S "$sock" "$@"
}

cleanup() {
	sentinel kill-server >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

sentinel -f /dev/null new-session -d -s sentinel -x 80 -y 24 'sleep 86400'
server_pid=$(sentinel display-message -p -t sentinel '#{pid}')
pane=$(sentinel display-message -p -t sentinel '#{pane_id}')

snapshot() {
	sentinel list-sessions -F 'session #{session_id} #{session_name} windows=#{session_windows}' &&
		sentinel list-panes -a -F 'pane #{session_name}:#{window_index}.#{pane_index} #{pane_id} pid=#{pane_pid}'
}

before=$(snapshot)
echo "test-tmux-isolation: sentinel server pid $server_pid on $sock" >&2
echo "$before" | sed 's/^/  before: /' >&2

export TMUX="$sock,$server_pid,0"
export TMUX_PANE="$pane"
echo "test-tmux-isolation: running with TMUX=$TMUX TMUX_PANE=$TMUX_PANE: $*" >&2

status=0
"$@" || status=$?

unset TMUX TMUX_PANE
if ! after=$(snapshot 2>&1); then
	echo "test-tmux-isolation: FAIL: the sentinel server is gone: $after" >&2
	exit 3
fi
echo "$after" | sed 's/^/  after:  /' >&2
if [ "$before" != "$after" ]; then
	echo "test-tmux-isolation: FAIL: the suite changed the sentinel server" >&2
	diff <(echo "$before") <(echo "$after") >&2 || true
	exit 3
fi
if ! kill -0 "$server_pid" 2>/dev/null; then
	echo "test-tmux-isolation: FAIL: sentinel server pid $server_pid is gone" >&2
	exit 3
fi
echo "test-tmux-isolation: PASS: sentinel server pid $server_pid and its sessions are untouched (command exit $status)" >&2
exit "$status"
