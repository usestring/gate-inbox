#!/usr/bin/env bash
# A stand-in agent for the resource-hog end-to-end test: a line prompt that
# logs every line typed into it, with a child process that holds the resource
# the test is about.
#
#   hogagent.sh <load> <screen> <dir>
#
# load:   cpu   one busy loop
#         mem   600 MiB written and held
#         grow  6 MiB more every second, up to 420 MiB, then held
#         none  nothing
# screen: prompt  a "$ " prompt from the start
#         dialog  a confirm dialog until <dir>/go exists, then the prompt
# dir:    <dir>/typed.log gets every line read; <dir>/load.pid the child's pid
#
# HOG_FANOUT=N adds N idle children, for a tree the size of a real agent's.
# HOG_NICE=N runs a cpu load at that niceness.
set -u
load=$1 screen=$2 dir=$3

for _ in $(seq 1 "${HOG_FANOUT:-0}"); do sleep 1000000 & done

case $load in
cpu) nice -n "${HOG_NICE:-0}" sh -c 'while :; do :; done' & ;;
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
