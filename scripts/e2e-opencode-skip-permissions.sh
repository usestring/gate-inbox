#!/usr/bin/env bash
# Prove an opencode session launched with skip permissions on runs without a
# prompt, and one launched with it off still prompts, against the real
# opencode v2 on this machine. Not run in CI: it needs opencode, a provider
# login and model calls.
#
# It builds this checkout, starts a throwaway board on a private tmux server
# with its own GATE_INBOX_HOME and its own opencode config, data and service
# port, so it cannot see or touch any other board, tmux server or opencode
# service. Through the board's New Session card it creates two opencode
# sessions in scratch repositories whose opencode.json asks for every shell
# and edit action, adds an agent-level ask and denies `rm *`:
#
#   perms on:  the shell write and the edit happen with no prompt, the `rm`
#              is still denied, and the session's OPENCODE_CONFIG lists the
#              skip-permissions plugin;
#   perms off: opencode stops on "Permission required", nothing is written,
#              and its config lists no plugin.
#
# Usage:
#   scripts/e2e-opencode-skip-permissions.sh --auth ~/.local/share/opencode/auth.json \
#       [--model provider/model] [--keep]
#
# --auth is copied into the scratch data directory so opencode can reach a
# model; --model picks one (opencode's default otherwise). --keep leaves the
# scratch directory and the private tmux server up for a look. Exit status 0
# when every check passed, 1 when one failed, 2 on a usage or setup error.
set -euo pipefail

auth="" model="" keep=0
while [ $# -gt 0 ]; do
	case "$1" in
	--auth) auth=$2; shift 2 ;;
	--model) model=$2; shift 2 ;;
	--keep) keep=1; shift ;;
	*) sed -n '2,28p' "$0" >&2; exit 2 ;;
	esac
done
if [ -z "$auth" ] || [ ! -f "$auth" ]; then
	echo "e2e: --auth must name an opencode auth.json" >&2
	exit 2
fi
for bin in opencode tmux go python3; do
	command -v "$bin" >/dev/null 2>&1 || { echo "e2e: $bin is not installed" >&2; exit 2; }
done
case "$(opencode --version 2>/dev/null)" in
*v2.*) ;;
*) echo "e2e: needs opencode v2" >&2; exit 2 ;;
esac

repo=$(cd "$(dirname "$0")/.." && pwd)
# Short on purpose: a tmux socket path is capped near 104 bytes.
work=$(mktemp -d "${TMPDIR:-/tmp}/gioc.XXXX")
sock=gioc

cleanup() {
	if [ "$keep" = 1 ]; then
		echo "e2e: kept $work (tmux -L $sock, TMUX_TMPDIR=$work/t)" >&2
		return
	fi
	run tmux -L "$sock" kill-server 2>/dev/null || true
	# opencode detaches a background service that outlives the tmux server;
	# stop the ones running on this run's data directory and nothing else.
	local p
	for p in $(pgrep -x opencode 2>/dev/null; pgrep -f 'opencode serve' 2>/dev/null); do
		tr '\0' '\n' <"/proc/$p/environ" 2>/dev/null | grep -qx "XDG_DATA_HOME=$work/data" && kill "$p" 2>/dev/null
	done
	sleep 1
	rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$work"/{bin,home,t,cfg/opencode,data/opencode,state,cache}
chmod 700 "$work/t"
cp "$auth" "$work/data/opencode/auth.json"
chmod 600 "$work/data/opencode/auth.json"
# The fixed managed-service port would collide with the operator's own
# opencode service, and without a service of its own the board cannot
# capture session ids.
port=$((40000 + RANDOM % 20000))
printf '{ "port": %d }\n' "$port" >"$work/cfg/opencode/service.json"
printf '{ "session": { "permissions": "ask" } }\n' >"$work/cfg/opencode/cli.json"
if [ -n "$model" ]; then
	printf '{ "autoupdate": false, "share": "disabled", "model": "%s" }\n' "$model" >"$work/cfg/opencode/opencode.jsonc"
else
	printf '{ "autoupdate": false, "share": "disabled" }\n' >"$work/cfg/opencode/opencode.jsonc"
fi

(cd "$repo" && go build -o "$work/bin/gate-inbox" .)

run() {
	env -i HOME="$HOME" TERM=xterm-256color LANG=C.UTF-8 \
		PATH="$work/bin:$(dirname "$(command -v opencode)"):/usr/local/bin:/usr/bin:/bin" \
		TMUX_TMPDIR="$work/t" GATE_INBOX_HOME="$work/home" GATE_INBOX_TMUX_SOCKET="$sock" \
		XDG_CONFIG_HOME="$work/cfg" XDG_DATA_HOME="$work/data" \
		XDG_STATE_HOME="$work/state" XDG_CACHE_HOME="$work/cache" "$@"
}
t() { run tmux -L "$sock" "$@"; }
screen() { t capture-pane -p -t "$1"; }

mkrepo() {
	local dir="$work/r/$1"
	mkdir -p "$dir"
	git -C "$dir" init -q
	git -C "$dir" -c user.name=e2e -c user.email=e2e commit -q --allow-empty -m init
	cat >"$dir/opencode.json" <<'JSON'
{
  "$schema": "https://opencode.ai/config.json",
  "permissions": [
    { "action": "shell", "resource": "*", "effect": "ask" },
    { "action": "shell", "resource": "rm *", "effect": "deny" }
  ],
  "agents": {
    "build": { "permissions": [ { "action": "edit", "resource": "*", "effect": "ask" } ] }
  }
}
JSON
	echo keep >"$dir/deny-target.txt"
	echo "$dir"
}

# new_session NAME DIR on|off: fill the New Session card and create the
# session, printing its tmux session name.
new_session() {
	local name=$1 dir=$2 perms=$3 before prompt
	prompt="Do these steps in order, one tool call each, and do not ask me anything: (1) run the shell command: echo hello > $dir/shell-proof.txt (2) use your edit or write tool to create $dir/edit-proof.txt containing edited (3) run the shell command: rm $dir/deny-target.txt"
	before=$(t list-sessions -F '#{session_name}' | sort)
	while screen board | grep -q 'focused · ctrl+q'; do t send-keys -t board C-q; sleep 1; done
	t send-keys -t board C-n
	wait_for 10 "screen board | grep -q 'New Session'" || { echo "e2e: New Session card did not open" >&2; return 1; }
	t send-keys -t board -l opencode
	sleep 1
	t send-keys -t board Down Down
	[ "$perms" = on ] && t send-keys -t board Space
	t send-keys -t board Down Down
	t send-keys -t board -l "$name"
	t send-keys -t board Down
	t send-keys -t board -l "$prompt"
	t send-keys -t board Tab Tab C-u
	t send-keys -t board -l "$dir"
	sleep 1.5
	# The directory field opens a completion list; close it before creating.
	screen board | grep -q 'esc close' && t send-keys -t board Escape
	sleep 1
	screen board | grep -q 'perms .*skip all prompts' && [ "$perms" = off ] && { echo "e2e: perms toggle is on for $name" >&2; return 1; }
	t send-keys -t board Enter
	wait_for 30 "[ -n \"\$(comm -13 <(echo \"$before\") <(t list-sessions -F '#{session_name}' | sort) | grep '^gi_')\" ]" || return 1
	comm -13 <(echo "$before") <(t list-sessions -F '#{session_name}' | sort) | grep '^gi_' | head -1
}

wait_for() {
	local deadline=$((SECONDS + $1))
	until eval "$2"; do
		[ $SECONDS -ge $deadline ] && return 1
		sleep 2
	done
}

plugins_of() {
	local pid cfg
	pid=$(t display -p -t "$1" '#{pane_pid}')
	cfg=$(for p in $pid $(pgrep -P "$pid"); do tr '\0' '\n' <"/proc/$p/environ" 2>/dev/null | sed -n 's/^OPENCODE_CONFIG=//p'; done | head -1)
	python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("plugins") or "")' "$cfg"
}

fails=0
check() {
	if eval "$2"; then
		echo "PASS  $1"
	else
		echo "FAIL  $1"
		fails=$((fails + 1))
		screen "${3:-board}" | grep -v '^[[:space:]┃│]*$' | tail -15 | sed 's/^/      /'
	fi
}

on=$(mkrepo on)
off=$(mkrepo off)
run tmux -L "$sock" new-session -d -s board -x 200 -y 50 -c "$on" gate-inbox
wait_for 30 "screen board | grep -qi 'gate inbox'" || { echo "e2e: board did not start" >&2; exit 2; }
t send-keys -t board Escape
sleep 1

son=$(new_session skip-on "$on" on) || { echo "e2e: could not create the perms-on session" >&2; exit 2; }
soff=$(new_session skip-off "$off" off) || { echo "e2e: could not create the perms-off session" >&2; exit 2; }

wait_for 240 "[ -f '$on/edit-proof.txt' ] || screen $son | grep -q 'Permission required'" || true
wait_for 240 "screen $soff | grep -q 'Permission required'" || true
sleep 5

check "on: shell write ran" "[ -f '$on/shell-proof.txt' ]" "$son"
check "on: edit ran despite the agent-level ask" "[ -f '$on/edit-proof.txt' ]" "$son"
check "on: explicit deny still denied rm" "[ -f '$on/deny-target.txt' ]" "$son"
check "on: no permission prompt" "! screen $son | grep -q 'Permission required'" "$son"
check "on: config lists the skip-permissions plugin" "plugins_of $son | grep -q opencode-skip-permissions" "$son"
check "off: permission prompt shown" "screen $soff | grep -q 'Permission required'" "$soff"
check "off: nothing written" "[ ! -f '$off/shell-proof.txt' ] && [ ! -f '$off/edit-proof.txt' ]" "$soff"
check "off: config lists no plugin" "! plugins_of $soff | grep -q opencode-skip-permissions" "$soff"

[ "$fails" -eq 0 ] || exit 1
