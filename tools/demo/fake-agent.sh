#!/usr/bin/env bash
# A scripted stand-in for an agent CLI, for the README recordings. It draws a
# Claude-style prompt, a working spinner and a numbered permission dialog, so
# the board's pane rules read it as working, waiting and finished, but it
# calls no model and reads no account: every reply is canned below.
#
# The launcher appends the opening prompt as the last argument, and the id the
# board minted after --session-id. The first turn names the session through
# `gate-inbox rename`, the way a real agent answers the board's naming
# request, and every turn is appended to a Claude Code-shaped transcript under
# $HOME/.claude, which is what the board's message preview reads.
set -uo pipefail

cwd="${PWD/#$HOME/\~}"
esc=$'\e'
id=""
prompt=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --session-id) id="${2:-}"; shift 2 ;;
    -*) shift ;;
    *) prompt="$1"; shift ;;
  esac
done
# The board puts its own notes ahead of the task; the task is the last line.
prompt="$(printf '%s\n' "$prompt" | sed '/^[[:space:]]*$/d' | tail -n 1)"

transcript=""
if [[ -n "$id" ]]; then
  transcript="$HOME/.claude/projects/$(printf '%s' "$PWD" | tr -c 'a-zA-Z0-9' '-')/$id.jsonl"
  mkdir -p "$(dirname "$transcript")"
fi
json() { local t="${1//\\/\\\\}"; printf '%s' "${t//\"/\\\"}"; }
record() { # record <user|assistant> <text>
  [[ -n "$transcript" ]] || return 0
  if [[ "$1" == user ]]; then
    printf '{"type":"user","sessionId":"%s","message":{"role":"user","content":"%s"}}\n' \
      "$id" "$(json "$2")" >>"$transcript"
  else
    printf '{"type":"assistant","sessionId":"%s","message":{"role":"assistant","content":[{"type":"text","text":"%s"}]}}\n' \
      "$id" "$(json "$2")" >>"$transcript"
  fi
}

say() { printf '%s\n' "$*"; sleep 0.25; }
# reply: a line of prose from the agent, on the pane and in the transcript.
reply() { say "● $1"; record assistant "$1"; }

banner() {
  printf '\e[38;5;173m╭──────────────────────────────────────────────╮\e[0m\n'
  printf '\e[38;5;173m│\e[0m \e[1m✻ agent\e[0m  scripted demo CLI                   \e[38;5;173m│\e[0m\n'
  printf '\e[38;5;173m│\e[0m   %-43s\e[38;5;173m│\e[0m\n' "$cwd"
  printf '\e[38;5;173m╰──────────────────────────────────────────────╯\e[0m\n\n'
}

# work <seconds> <verb>: the spinner line the board's "working" rule matches.
work() {
  local secs="$1" verb="$2" i glyphs=(✢ ✳ ✶ ✻ ✽ ✻ ✶ ✳)
  for ((i = 1; i <= secs; i++)); do
    printf '\r\e[K\e[38;5;173m%s\e[0m %s… (%ds · esc to interrupt)' "${glyphs[i % 8]}" "$verb" "$i"
    sleep 1
  done
  printf '\r\e[K'
}

# ask <title> <detail> <question> <option>...: a numbered dialog, answered
# with a digit, the arrows and Enter, or Esc. Sets $choice to the option number.
ask() {
  local title="$1" detail="$2" question="$3"
  shift 3
  local opts=("$@") sel=0 n=$# k rest i
  printf '\e[38;5;244m%s\e[0m\n' '────────────────────────────────────────────────'
  printf ' \e[1m%s\e[0m\n\n   %s\n\n %s\n' "$title" "$detail" "$question"
  draw() {
    for i in "${!opts[@]}"; do
      if [[ $i -eq $sel ]]; then
        printf '\r\e[K \e[36m❯ %d. %s\e[0m\n' $((i + 1)) "${opts[i]}"
      else
        printf '\r\e[K   %d. %s\n' $((i + 1)) "${opts[i]}"
      fi
    done
    printf '\r\e[K\n\e[38;5;244mEnter to confirm · Esc to cancel\e[0m'
    # Park the caret on the highlighted row's marker, as Claude Code does:
    # the board reads a caret there as a selection dialog being up.
    printf '\e[%dA\r\e[1C' $((n - sel + 1))
  }
  unpark() { printf '\e[%dB\r' $((n - sel + 1)); }
  draw
  while true; do
    IFS= read -rsn1 k
    case "$k" in
      [1-9]) [[ $k -le $n ]] && { unpark; sel=$((k - 1)); break; } ;;
      '') unpark; break ;;
      "$esc")
        IFS= read -rsn2 -t 0.05 rest || true
        unpark
        case "$rest" in
          '[A') ((sel > 0)) && sel=$((sel - 1)) ;;
          '[B') ((sel < n - 1)) && sel=$((sel + 1)) ;;
          '') sel=$((n - 1)); break ;;
        esac
        printf '\e[%dA' $((n + 1))
        draw
        ;;
    esac
  done
  # Clear the dialog, the way a CLI replaces it with the outcome.
  printf '\r\e[K\e[%dA' $((n + 7))
  printf '\e[J'
  choice=$((sel + 1))
}

rename_once() {
  [[ -n "${renamed:-}" ]] && return
  renamed=1
  [[ -n "${GATE_INBOX_BIN:-}" && -n "$1" ]] && "$GATE_INBOX_BIN" rename "$1" >/dev/null 2>&1 &
}

done_turn() { printf '\e[38;5;173m✻\e[0m Worked for %s\n\n' "$1"; }

turn() {
  local text="$1"
  printf '\e[38;5;244m❯ %s\e[0m\n\n' "${text//$'\n'/$'\n'  }"
  record user "$text"
  case "$text" in
    *flaky*|*login*)
      rename_once fix-flaky-login-test
      work 3 Reading
      say "● Read(tests/login.test.ts)"
      say "  └  Read 112 lines"
      work 3 Investigating
      reply "The session fixture is shared between tests, so two logins race."
      say "  I scoped it per test. Running the suite to confirm."
      record assistant "I scoped it per test. Running the suite to confirm."
      echo
      ask "Bash command" "npm test -- login" "Do you want to proceed?" \
        "Yes" "Yes, and don't ask again for npm test commands" "No, tell the agent what to do differently"
      if [[ $choice -eq 3 ]]; then
        say "  └  Interrupted · What should the agent do instead?"
        echo
        return
      fi
      say "● Bash(npm test -- login)"
      work 3 Testing
      say "  └  24 passed, 0 failed (3 runs)"
      reply "Fixed: each login test now gets its own session fixture."
      done_turn 38s
      ;;
    *ABC-123*|*export*)
      rename_once abc-123-csv-export
      work 3 Planning
      say "● Read(src/billing/export.ts)"
      reply "Adding the CSV export behind the existing billing route."
      say "● Update(src/billing/export.ts)"
      say "  └  Added 41 lines, removed 6 lines"
      work 900 Implementing
      ;;
    *README*|*readme*|*docs*)
      rename_once docs-install-steps
      work 3 Reading
      say "● Update(README.md)"
      say "  └  Added 12 lines, removed 3 lines"
      reply "The install section now covers go install and a local build."
      done_turn 21s
      ;;
    *dependenc*|*upgrade*|*bump*)
      rename_once upgrade-http-client
      work 3 Reading
      say "● Bash(npm outdated)"
      say "  └  http-client 3.2.1 → 4.0.0 (major)"
      reply "4.0.0 drops the callback API that 3 files still use."
      echo
      ask "Question" "4.0.0 drops the callback API used in 3 files." "Upgrade to the new major version?" \
        "Yes, migrate the 3 call sites" "Stay on 3.x and take the patch release" "No, tell the agent what to do differently"
      case $choice in
        1) reply "Migrating 3 call sites to the promise API."; work 4 Updating; done_turn 52s ;;
        2) reply "Pinned http-client to ^3.2.4."; work 2 Updating; done_turn 17s ;;
        *) say "  └  Interrupted · What should the agent do instead?"; echo ;;
      esac
      ;;
    *loop*|*times*)
      work 3 Writing
      say "● Write(tests/login.race.test.ts)"
      say "  └  Wrote 12 lines"
      for l in "import { login } from '../src/auth';" "import { freshSession } from './fixtures';" "" \
        "test('two logins at once keep their own sessions', async () => {" \
        "  const [a, b] = await Promise.all([" "    login(freshSession(), 'ada')," \
        "    login(freshSession(), 'grace')," "  ]);" "  expect(a.user).toBe('ada');" \
        "  expect(b.user).toBe('grace');" "  expect(a.id).not.toBe(b.id);" "});"; do
        printf '      %s\n' "$l"
        sleep 0.08
      done
      say "● Bash(for i in \$(seq 20); do npm test -- login.race || break; done)"
      work 2 Running
      for ((i = 1; i <= 20; i++)); do
        printf '  run %2d/20  ✓ 3 passed (0.4s)\n' "$i"
        sleep 0.12
      done
      reply "20 of 20 runs passed: the race no longer reproduces with a fixture per test."
      done_turn "1m 12s"
      ;;
    *test*)
      work 3 Writing
      say "● Write(tests/login.race.test.ts)"
      say "  └  Wrote 28 lines"
      reply "Added a regression test that runs two logins in parallel."
      done_turn 14s
      ;;
    *)
      work 3 Thinking
      reply "Done. Nothing else is pending on this branch."
      done_turn 6s
      ;;
  esac
}

clear
banner
[[ -n "$prompt" ]] && turn "$prompt"
while true; do
  printf '\e[38;5;244m%s\e[0m\n' "────────────────────────────────────────────────"
  IFS= read -r -e -p '❯ ' line || exit 0
  # A trailing backslash continues the message on the next line, as it does
  # in Claude Code.
  cols="$(tput cols 2>/dev/null || echo 80)"
  rows=$(((2 + ${#line} + cols - 1) / cols + 1))
  while [[ "$line" == *\\ ]]; do
    IFS= read -r -e -p '  ' more || break
    rows=$((rows + (2 + ${#more} + cols - 1) / cols))
    line="${line%\\}"$'\n'"$more"
  done
  # Take the rule and the prompt back off, however many rows it wrapped onto;
  # turn() redraws it as history.
  printf '\e[%dA\e[J' "$rows"
  # The board's own notes to a fresh session are not the user's task.
  [[ -z "$line" || "$line" == "[gate-inbox]"* ]] && continue
  turn "$line"
done
