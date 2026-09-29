#!/usr/bin/env bash
# A stand-in agent for the resource-hog end-to-end test: a line prompt that
# logs every line typed into it, with a child process that holds the resource
# the test is about.
#
#   hogagent.sh <load> <screen> <dir>
#
# load:   cpu   one busy loop
#         browser  a headless Chrome on about:blank: a dozen processes with
#               terabyte address spaces, the costliest thing to read PSS from
#         mem   600 MiB written and held
#         grow  6 MiB more every second, up to 420 MiB, then held
#         none  nothing
# screen: prompt  a "$ " prompt from the start
#         dialog  a confirm dialog until <dir>/go exists, then the prompt
# dir:    <dir>/typed.log gets every line read; <dir>/load.pid the load's pid,
#         <dir>/fanout.pids the idle children's, for the test to reap
#
# HOG_FANOUT=N adds N idle children, for a tree the size of a real agent's.
# HOG_NICE=N runs a cpu load at that niceness.
set -u
load=$1 screen=$2 dir=$3

for _ in $(seq 1 "${HOG_FANOUT:-0}"); do
	sleep 1000000 &
	echo $! >>"$dir/fanout.pids"
done

case $load in
cpu) nice -n "${HOG_NICE:-0}" sh -c 'while :; do :; done' & ;;
browser) google-chrome --headless=new --no-sandbox --disable-gpu --user-data-dir="$dir/chrome" about:blank >/dev/null 2>&1 & ;;
mem) python3 -c 'import time; b = b"x" * (600 << 20); time.sleep(10**6)' & ;;
grow) python3 -c '
import time
held = []
while len(held) < 70:
    held.append(b"x" * (6 << 20))
    time.sleep(1)
time.sleep(10**6)' & ;;
esac
[ "$load" != none ] && echo $! >"$dir/load.pid"

if [ "$screen" = dialog ]; then
	printf '  Do you want to proceed?\n  ❯ 1. Yes\n    2. No\n\n  Enter to confirm · Esc to cancel\n'
	until [ -e "$dir/go" ]; do sleep 0.2; done
	clear
fi

printf '$ '
while IFS= read -r line; do
	printf '%s\n' "$line" >>"$dir/typed.log"
	printf '$ '
done
