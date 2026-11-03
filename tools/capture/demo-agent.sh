#!/usr/bin/env bash
# Stand-in for a real agent CLI inside a capture run: replays a canned
# transcript into the pane and then idles, so a recording shows a board with
# plausible session content and no live agent, network call or token spend.
# Every argument the launcher appends (--session-id, --model, a prompt) is
# ignored on purpose; the first argument names the transcript.
set -uo pipefail

transcript="${1:-working}"
if [[ "$transcript" == "composer-submit" ]]; then
  stty -echo -icanon 2>/dev/null || true
  printf 'Ready for a prompt\n❯ '
  IFS= read -r _prompt
  sleep 0.25
  if [[ -n "${GATE_INBOX_STATUS_FILE:-}" ]]; then
    printf 'working UserPromptSubmit\n' >> "$GATE_INBOX_STATUS_FILE"
  fi
  printf '\033[2J\033[H✻ Working… (esc to interrupt)\n• Working (0s • esc to interrupt)\n'
  cat >/dev/null
  exit 0
fi
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# "ask-free-text" is a Claude Code AskUserQuestion with a "Type something."
# row that takes its answer the way the real one does: the arrows move the
# cursor, words typed on the free-text row become the answer, and Enter
# prints the record the board reads the answer back from.
if [[ "$transcript" == "ask-free-text" ]]; then
  stty -echo -icanon 2>/dev/null || true
  # Short enough that the answered record fits one row: a record wrapped
  # mid-word reads back with a space in the answer and fails the check.
  question="Which rollout path should I take?"
  opts=("Staged UI first" "Only region 4021" "Type something.")
  cur=1
  draw() {
    printf '\033[2J\033[H● Two ways to roll this out; I want your call first.\n\n'
    printf '────────────────────────────────────────\n ☐ Rollout \n\n%s\n\n' "$question"
    for i in "${!opts[@]}"; do
      if ((i + 1 == cur)); then printf '❯ %d. %s\n' $((i + 1)) "${1:-${opts[i]}}"; else printf '  %d. %s\n' $((i + 1)) "${opts[i]}"; fi
    done
    printf '\nEnter to select · ↑/↓ to navigate · Esc to cancel\n'
  }
  draw
  answer=""
  while IFS= read -rsn1 key; do
    if [[ $key == $'\e' ]]; then
      read -rsn2 rest
      case $rest in
        '[A') ((cur > 1)) && cur=$((cur - 1)) ;;
        '[B') ((cur < ${#opts[@]})) && cur=$((cur + 1)) ;;
      esac
      draw
    elif [[ -z $key ]]; then
      [[ -z $answer ]] && answer="${opts[cur-1]}"
      break
    elif ((cur == ${#opts[@]})); then
      answer+="$key"
      draw "$answer"
    fi
  done
  sleep 0.6
  printf '\033[2J\033[H● User answered Claude'"'"'s questions:\n  ⎿  · %s → %s\n\n' "$question" "$answer"
  printf '✻ Working… (esc to interrupt)\n'
  cat >/dev/null
  exit 0
fi
if [[ "$transcript" == "opencode-custom-answer" ]]; then
  stty -echo -icanon 2>/dev/null || true
  printf '\033[2J\033[H'
  cat <<'DIALOG'
  ┃
  ┃  Questions
  ┃
  ┃  Which release target should we use?
  ┃
  ┃  1. Staging
  ┃  2. Production
  ┃  3. Canary
  ┃  4. Type your own answer
  ┃
  ┃  ↑↓ select  enter submit  esc dismiss
  ┃
DIALOG
  IFS= read -r _key
  printf '\033[2J\033[H'
  cat <<'CUSTOM'
  ┃
  ┃  Questions
  ┃
  ┃  Which release target should we use?
  ┃
  ┃  1. Staging
  ┃  2. Production
  ┃  3. Canary
  ┃  4.
  ┃
  ┃  enter submit  esc close
  ┃
CUSTOM
  printf '\033[9;10H'
  stty echo
  IFS= read -r _answer
  stty -echo
  printf '\033[2J\033[HWorking on the supplied answer\n⬝⬝⬝⬝ esc interrupt\n'
  cat >/dev/null
  exit 0
fi
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

# "handover-late" is an answer the check after the key cannot confirm: a late
# Notification hook reads as a fresh dialog, and the agent only goes back to
# work after the window has closed. Only the poll's transition is left to see.
if [[ "$transcript" == "handover-late" ]]; then
  stty -echo -icanon 2>/dev/null || true
  printf '\033[4A\033[1G'
  IFS= read -r -n 1 _key
  printf 'waiting Notification\n' >> "${GATE_INBOX_STATUS_FILE:?}"
  sleep 4
  printf 'working PostToolUse\n' >> "${GATE_INBOX_STATUS_FILE:?}"
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
