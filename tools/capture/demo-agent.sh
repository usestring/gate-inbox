#!/usr/bin/env bash
# Stand-in for a real agent CLI inside a capture run: replays a canned
# transcript into the pane and then idles, so a recording shows a board with
# plausible session content and no live agent, network call or token spend.
# Every argument the launcher appends (--session-id, --model, a prompt) is
# ignored on purpose; the first argument names the transcript.
set -uo pipefail

transcript="${1:-working}"
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
file="$here/demo/$transcript.txt"

if [[ -r "$file" ]]; then
  while IFS= read -r line; do
    printf '%s\n' "$line"
    sleep 0.12
  done <"$file"
  # A transcript file whose last line carries no trailing newline stops there
  # on purpose: it is how a scenario leaves the pane's caret at the end of the
  # line the agent last drew, which is where a resting CLI parks it. With a
  # trailing newline the caret falls to the blank row below, and a change that
  # reads the caret's position -- what Left or Right means at a prompt -- has
  # no frame it can be recorded in. The read above stops at EOF with that
  # partial line still in $line, so printing it here is the whole of it.
  if [[ -n "${line:-}" ]]; then
    printf '%s' "$line"
  fi
else
  printf 'demo-agent: no transcript %s\n' "$transcript"
fi

if [[ "$transcript" == "handover" ]]; then
  stty -echo -icanon 2>/dev/null || true
  printf '\033[4A\033[1G'
  IFS= read -r -n 1 _key
  printf 'working PostToolUse\n' >> "${GATE_INBOX_STATUS_FILE:?}"
  IFS= read -r -n 1 _key
  printf '\033[2J\033[H✻ Working… (esc to interrupt)\n'
  cat >/dev/null
  exit 0
fi

# Hold the pane open: the board reads a session's state from its pane, and a
# command that exits would read as a dead session rather than a live one.
# "working" keeps emitting, because the board derives that state from a region
# that changed since the previous poll rather than from any one line.
if [[ "$transcript" == "working" ]]; then
  n=0
  while true; do
    n=$((n + 1))
    printf '● Rendering frame %d\n' "$n"
    sleep 2
  done
fi
# The "deaf" transcript is the one that reads its input and never paints:
# it stands in for an agent whose TUI has wedged, which is the only way to
# record what the board does about one. Everything else about the pane is a
# live process, because that is what the real failure looks like.
if [[ "$transcript" == "deaf" ]]; then
  # -echo -icanon is what makes it deaf rather than merely silent: without it
  # the tty's own line discipline echoes every character, the pane repaints,
  # and the board is right to call it alive. A real TUI holds the terminal in
  # exactly this mode and does its own drawing -- a wedged one just never does
  # the drawing.
  stty -echo -icanon 2>/dev/null || true
  cat >/dev/null
  exit 0
fi

# A parked agent echoes what is typed at it, the way a real one prints the
# prompt it was given, so a scenario can put a pull request URL or a ticket
# id on the pane and the board's work tracker finds it there. Not exec'd: the
# adoption scan identifies a pane by the program in its process tree, and a
# --foreign pane is only recognisable as this script while this script is
# still the parent of what the pane runs.
cat
