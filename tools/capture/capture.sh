#!/usr/bin/env bash
# Record the real gate-inbox TUI running a scenario, into a GIF, a WebM and
# PNG stills laid out for skills/ship-ui-feature/scripts/post-to-pr.ts.
#
#   tools/capture/capture.sh --scenario tools/capture/scenarios/board-tour.tape
#   tools/capture/capture.sh --scenario <tape> --config <toml appended to the scratch config>
#
# The board runs against a scratch GATE_INBOX_HOME on a private tmux server, so
# a capture never sees, moves or kills a session on the operator's live board.
# Exit 2 means a tool this needs is missing and nothing was changed.
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd -- "$here/../.." && pwd)"

scenario="$here/scenarios/board-tour.tape"
out=""
width=""
height=""
cols=""
rows=""
# The terminal is sized in cells, so the font size is the whole resolution
# knob: pixels are cells times cell size. 20 keeps the GIF legible at the
# width GitHub renders a PR body in.
font_size=20
keep_home=0
# OLED by default: pure black is what a capture reads cleanest in on both
# GitHub themes. --theme current records whatever the operator runs instead.
theme="oled"
# The terminal's own background before the board starts, when a capture has to
# show that the board covers every cell rather than sitting on a terminal that
# already matches it. Empty takes the theme's.
terminal_bg=""
# Which canned transcript each seeded tool plays, in the order the tools are
# found. The default set draws one row per resting state; a scenario about a
# particular pane shape names its own transcript for the row it looks at.
transcripts=(waiting working finished)
# Panes the manager did not start: each --foreign opens a window on the
# board's own tmux server, before the board comes up, running the demo agent
# on the named transcript. The adoption scan takes it, which is how a
# scenario about adopted panes gets some. --no-seed skips the spawned trio,
# for a scenario whose board is those panes alone.
foreign=()
no_seed=0
codex_question=""

die() { printf 'capture: %s\n' "$1" >&2; exit 1; }
missing() { printf 'capture: %s\n' "$1" >&2; exit 2; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --scenario) scenario="${2:?}"; shift 2 ;;
    --out) out="${2:?}"; shift 2 ;;
    --width) width="${2:?}"; shift 2 ;;
    --height) height="${2:?}"; shift 2 ;;
    --font-size) font_size="${2:?}"; shift 2 ;;
    --cols) cols="${2:?}"; shift 2 ;;
    --rows) rows="${2:?}"; shift 2 ;;
    --keep-home) keep_home=1; shift ;;
    --theme) theme="${2:?}"; shift 2 ;;
    --terminal-bg) terminal_bg="${2:?}"; shift 2 ;;
    --config) extra_config="${2:?}"; shift 2 ;;
    --transcripts) IFS=, read -r -a transcripts <<<"${2:?}"; shift 2 ;;
    --foreign) foreign+=("${2:?}"); shift 2 ;;
    --no-seed) no_seed=1; shift ;;
    --codex-question) codex_question="${2:?}"; shift 2 ;;
    -h|--help) sed -n '2,10p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) die "unknown flag $1" ;;
  esac
done

[[ -r "$scenario" ]] || die "no scenario at $scenario"
[[ -z "${extra_config:-}" || -r "$extra_config" ]] || die "no config at $extra_config"

command -v vhs >/dev/null || missing "vhs is not on PATH — go install github.com/charmbracelet/vhs@latest"
command -v ttyd >/dev/null || missing "ttyd is not on PATH — apt-get install ttyd (vhs drives it)"
command -v ffmpeg >/dev/null || missing "ffmpeg is not on PATH — apt-get install ffmpeg (vhs encodes with it)"
command -v tmux >/dev/null || missing "tmux is not on PATH"

# vhs screenshots the terminal through headless Chromium. Point it at a browser
# already on this machine rather than letting it download one, and wrap that in
# --no-sandbox: Chromium's own sandbox needs unprivileged user namespaces, which
# this box (and every container like it) does not hand out.
#
# A candidate has to start, not merely be executable. Homebrew's chromium cask
# leaves /opt/homebrew/bin/chromium on PATH as a wrapper around an app bundle
# the uninstall took with it, so an -x test picks a browser that dies the
# instant vhs launches it -- surfacing a minute into the run as "browser exited
# unexpectedly before its debugging endpoint was ready" rather than as the
# missing browser it is.
usable_chrome() { [[ -n "$1" && -x "$1" ]] && "$1" --version >/dev/null 2>&1; }
find_chrome() {
  local c
  for c in "$(command -v chrome || true)" "$(command -v google-chrome || true)" \
           "$(command -v chromium || true)" "$(command -v chromium-browser || true)" \
           "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
           "/Applications/Chromium.app/Contents/MacOS/Chromium" \
           "$HOME"/Library/Caches/ms-playwright/chromium-*/chrome-mac*/*.app/Contents/MacOS/* \
           "$HOME"/.cache/ms-playwright/chromium-*/chrome-linux*/chrome \
           "$HOME"/.cache/rod/browser/chromium-*/chrome; do
    usable_chrome "$c" && { printf '%s' "$c"; return 0; }
  done
  return 1
}
chrome="$(find_chrome)" || missing "no working Chromium found — install one, or run 'bunx playwright install chromium'"

run_id="$(date -u +%Y%m%d-%H%M%S)"
if [[ -z "$out" ]]; then
  branch="$(git -C "$root" rev-parse --abbrev-ref HEAD 2>/dev/null || echo capture)"
  out="/tmp/claude/gate-capture/${branch//\//-}"
fi
# vhs's tape parser rejects an absolute path after Output/Screenshot, so every
# media path in the tape is relative and vhs runs with $out as its directory.
rel_media="media/$run_id"
rel_video="test-results/$run_id"
media="$out/$rel_media"
video_dir="$out/$rel_video"
work="$out/work"
mkdir -p "$media" "$video_dir" "$work/bin"

shim="$work/bin/chrome"
# Quoted: a macOS browser lives inside an .app bundle, and every one of those
# paths has a space in it.
printf '#!/bin/sh\nexec "%s" --no-sandbox --disable-dev-shm-usage "$@"\n' "$chrome" >"$shim"
chmod +x "$shim"

# The board's tmux children connect over a unix socket, and a socket path is
# capped near 108 bytes — a scratch directory under the run output is long
# enough to blow that, and tmux reports it as "File name too long".
sock="$(mktemp -d /tmp/gib-cap.XXXXXX)"
home="$work/home"
mkdir -p "$home"
rm -f "$home/state.db" "$home/state.db-shm" "$home/state.db-wal"

# Demo tools, not the operator's: every session a scenario opens runs the
# canned transcript player, so a capture spawns no real agent and spends no
# tokens. Only a CLI actually installed here can be picked — the board's
# agent picker lists nothing else — so the seeded set is whichever of these
# this machine has, and each one draws a different state on the board.
tools=()
for candidate in claude codex opencode; do
  command -v "$candidate" >/dev/null || continue
  tools+=("$candidate")
  [[ ${#tools[@]} -eq 3 ]] && break
done
[[ ${#tools[@]} -gt 0 ]] || missing "no agent CLI on PATH — the board can only spawn one it can find"

{
  printf 'poll_interval = "1s"\n'
  # The resume commands are overridden too, because the defaults for a real
  # tool are that tool's own binary: a revive or a takeover in a scenario
  # would otherwise launch the real CLI. The conversation id a revive
  # carries is the transcript to replay, which is what the sidecars below
  # hand a foreign pane.
  # The account commands are overridden for the same reason: a session
  # launched on a named account reads that account's token, and a capture
  # must reach no real secret store and put no real token in its work dir.
  for i in "${!tools[@]}"; do
    printf '\n[tools.%s]\ncommand = "%s/demo-agent.sh %s"\nresume_by_id_command = "%s/demo-agent.sh {id}"\nrevive_command = "%s/demo-agent.sh %s"\n' \
      "${tools[$i]}" "$here" "${transcripts[$i % ${#transcripts[@]}]}" "$here" "$here" "${transcripts[$i % ${#transcripts[@]}]}"
    printf 'account_command = "echo demo-token"\naccounts_command = "printf '"'"'%%s\\\\n'"'"' CLAUDE_OAUTH_TOKEN_ALICE1 CLAUDE_OAUTH_TOKEN_BOB2"\n'
  done
  # A scenario's own settings go last, so a run can shorten a window the
  # board measures in days to one a recording can wait out.
  if [[ -n "${extra_config:-}" ]]; then printf '\n'; cat "$extra_config"; fi
} >"$home/config.toml"

binary="$work/bin/gate-inbox"
# Stamp the commit the recording was made from. This is the one build of the
# board this repository controls -- an operator's own launcher typically runs `go run .`
# from a shell function, and Go stamps nothing under `go run` -- so a capture is
# also the only board whose spans can name a commit without being asked to.
# A tree with no git available still builds; the stamp is simply empty.
revision="$(cd "$root" && git rev-parse HEAD 2>/dev/null || true)"
(cd "$root" && GOWORK=off go build \
  -ldflags "-X github.com/usestring/gate-inbox/internal/tracing.Revision=$revision" \
  -o "$binary" .) || die "build failed"

# A foreign pane is a session on the board's server that is not gi_*: the
# scan lists that server, and a session by any other name is somebody
# else's. Opened before the board so its transcript has played out by the
# first poll, which is what leaves it reading as idle rather than working.
#
# Each gets the session file Claude Code writes for a live process, in a
# scratch CLAUDE_CONFIG_DIR the board is pointed at: that file is how a
# takeover reads the conversation off the pane, and the id it names is the
# transcript the relaunch replays.
claude_dir="$home/claude"
mkdir -p "$claude_dir/sessions"

# Codex reads its conversations out of CODEX_HOME, and without one pointed at
# the scratch tree the board would bind a seeded codex row to whichever of the
# operator's own rollouts last touched this cwd -- and then read that real
# conversation's questions and history. Scratch, like everything else here.
codex_dir="$home/codex"
mkdir -p "$codex_dir/sessions"

# --codex-question seeds a rollout holding one question in the given state, so
# a capture can show what the board does about a codex question nobody
# answered. The records are the ones taken off the live probe on 2026-09-09:
# the ask, and the {"answers":{}} its auto-resolution timer returned 123.2s
# later. "expired" is that pair; "open" is the ask with nothing back yet.
if [[ -n "$codex_question" ]]; then
  case "$codex_question" in
    expired|open) ;;
    *) die "--codex-question takes expired or open, not $codex_question" ;;
  esac
  cq_id="01a0846e-b46b-7482-8e6d-33ad3d8e09fe"
  cq_dir="$codex_dir/sessions/2026/09/09"
  mkdir -p "$cq_dir"
  {
    printf '{"type":"session_meta","payload":{"session_id":"%s","cwd":"%s","source":"cli","originator":"codex-tui"}}\n' \
      "$cq_id" "$root"
    head -1 "$root/internal/codexq/testdata/expired-blocking.jsonl"
    [[ "$codex_question" == "expired" ]] && sed -n '2p' "$root/internal/codexq/testdata/expired-blocking.jsonl"
  } >"$cq_dir/rollout-2026-09-09T04-30-42-$cq_id.jsonl"
  # Id capture only considers a rollout whose modtime is no more than
  # clockSlack (10s) older than the session's launch, and the seeding
  # keystrokes do not reach the board for the best part of a minute after
  # this file is written. A modtime in the future clears that window however
  # long the run takes to get there.
  touch -d '+1 hour' "$cq_dir/rollout-2026-09-09T04-30-42-$cq_id.jsonl"
  printf 'capture: seeded a %s codex question\n' "$codex_question"
fi
for i in "${!foreign[@]}"; do
	if [[ -r "$here/demo/${foreign[$i]}.jsonl" ]]; then
		project_dir="$(printf '%s' "$root" | sed 's/[^a-zA-Z0-9]/-/g')"
		mkdir -p "$claude_dir/projects/$project_dir"
		cp "$here/demo/${foreign[$i]}.jsonl" "$claude_dir/projects/$project_dir/${foreign[$i]}.jsonl"
	fi
  name="operator-$((i + 1))"
  env -u TMUX -u TMUX_PANE TMUX_TMPDIR="$sock" tmux -L default new-session -d \
    -s "$name" -c "$root" -x "${cols:-120}" -y "${rows:-40}" \
    "$here/demo-agent.sh ${foreign[$i]}" || die "foreign pane ${foreign[$i]} failed"
  pid="$(TMUX_TMPDIR="$sock" tmux -L default list-panes -t "$name" -F '#{pane_pid}')"
  printf '{"pid":%s,"sessionId":"%s","cwd":"%s","startedAt":%s,"kind":"cli"}\n' \
    "$pid" "${foreign[$i]}" "$root" "$(($(date +%s) * 1000))" >"$claude_dir/sessions/$pid.json"
done

# The theme is a settings row in the board's store, so seed the scratch one
# with the palette this capture should record in. Only that row is touched —
# never a session.
themeseed="$work/bin/themeseed"
(cd "$root" && GOWORK=off go build -o "$themeseed" ./tools/capture/themeseed) || die "themeseed build failed"
if [[ "$theme" == "current" ]]; then
  seeded="$("$themeseed" --from "${GATE_INBOX_HOME:-}" --to "$home")"
else
  seeded="$theme"
  "$themeseed" --to "$home" --set "$theme" >/dev/null
fi
printf 'capture: theme %s\n' "${seeded:-default}"

# The margin around the board and the board's own background are painted by
# different things, so a mismatch reads as a rendering bug. OLED gets a black
# terminal spelled out, since vhs ships no theme by that name; anything else
# takes the vhs theme of the same name when there is one.
vhs_theme=""
vhs_theme_json=""
if [[ "$seeded" == "oled" ]]; then
  vhs_theme_json='{ "name": "oled", "background": "#000000", "foreground": "#e6e6e6", "selection": "#303030", "cursor": "#e6e6e6", "black": "#000000", "red": "#ff5f5f", "green": "#5fd75f", "yellow": "#d7d75f", "blue": "#5f87ff", "magenta": "#d787d7", "cyan": "#5fd7d7", "white": "#e6e6e6", "brightBlack": "#5f5f5f", "brightRed": "#ff8787", "brightGreen": "#87ff87", "brightYellow": "#ffff87", "brightBlue": "#87afff", "brightMagenta": "#ffafff", "brightCyan": "#87ffff", "brightWhite": "#ffffff" }'
elif [[ -n "$seeded" ]]; then
  vhs_theme="$(vhs themes 2>/dev/null | grep -ixF "$seeded" | head -1 || true)"
fi
if [[ -n "$terminal_bg" ]]; then
  [[ "$terminal_bg" =~ ^#[0-9a-fA-F]{6}$ ]] || die "--terminal-bg wants #rrggbb, got $terminal_bg"
  vhs_theme=""
  vhs_theme_json='{ "name": "terminal", "background": "'"$terminal_bg"'", "foreground": "#e6e6e6", "selection": "#303030", "cursor": "#e6e6e6", "black": "#000000", "red": "#ff5f5f", "green": "#5fd75f", "yellow": "#d7d75f", "blue": "#5f87ff", "magenta": "#d787d7", "cyan": "#5fd7d7", "white": "#e6e6e6", "brightBlack": "#5f5f5f", "brightRed": "#ff8787", "brightGreen": "#87ff87", "brightYellow": "#ffff87", "brightBlue": "#87afff", "brightMagenta": "#ffafff", "brightCyan": "#87ffff", "brightWhite": "#ffffff" }'
fi

# Default to the terminal the operator is actually looking at, so a capture is
# the board at the size they run it rather than an arbitrary window.
if [[ -z "$cols" || -z "$rows" ]]; then
  read -r detected_cols detected_rows <<<"$(tmux display-message -p '#{client_width} #{client_height}' 2>/dev/null || true)"
  [[ "${detected_cols:-0}" -gt 20 ]] || detected_cols="$(tput cols 2>/dev/null || echo 0)"
  [[ "${detected_rows:-0}" -gt 10 ]] || detected_rows="$(tput lines 2>/dev/null || echo 0)"
  [[ "${detected_cols:-0}" -gt 20 ]] || detected_cols=160
  [[ "${detected_rows:-0}" -gt 10 ]] || detected_rows=45
  cols="${cols:-$detected_cols}"
  rows="${rows:-$detected_rows}"
fi

# vhs sizes its terminal in pixels and the board in cells, and nothing reports
# the cell size for a font at a given size — so measure it. Two probes at known
# pixel sizes separate the cell from the padding, which one probe cannot, and
# the result is cached per font size because it never moves for a given vhs.
cell_cache="$work/cell-$font_size.txt"
probe_cells() { # $1 width, $2 height -> "<cols> <rows>"
  local w="$1" h="$2" tape="$work/probe.tape"
  rm -f "$work/probe.txt"
  {
    printf 'Output work/probe.gif\n'
    printf 'Set Width %s\nSet Height %s\nSet FontSize %s\n' "$w" "$h" "$font_size"
    printf 'Type "echo ${COLUMNS} ${LINES} > work/probe.txt"\nEnter\nSleep 1500ms\n'
  } >"$tape"
  (cd "$out" && PATH="$work/bin:$PATH" vhs "$tape" >/dev/null 2>&1)
  cat "$work/probe.txt" 2>/dev/null
}
if [[ -z "$width" || -z "$height" ]]; then
  if [[ ! -r "$cell_cache" ]]; then
    printf 'capture: measuring the terminal cell at font size %s\n' "$font_size"
    read -r c1 r1 <<<"$(probe_cells 1400 800)"
    read -r c2 r2 <<<"$(probe_cells 2000 1000)"
    [[ "${c2:-0}" -gt "${c1:-0}" && "${r2:-0}" -gt "${r1:-0}" ]] || die "cell probe failed (got '${c1:-} ${r1:-}' then '${c2:-} ${r2:-}')"
    awk -v c1="$c1" -v r1="$r1" -v c2="$c2" -v r2="$r2" \
      'BEGIN { cw = 600 / (c2 - c1); ch = 200 / (r2 - r1); printf "%f %f %f %f\n", cw, 1400 - c1 * cw, ch, 800 - r1 * ch }' \
      >"$cell_cache"
  fi
  read -r cell_w pad_x cell_h pad_y <"$cell_cache"
  # Round up: a capture a cell wider than the terminal is invisible, a cell
  # narrower silently truncates the board's rightmost column.
  width="${width:-$(awk -v n="$cols" -v c="$cell_w" -v p="$pad_x" 'BEGIN { printf "%d", int(n * c + p + 0.999) }')}"
  height="${height:-$(awk -v n="$rows" -v c="$cell_h" -v p="$pad_y" 'BEGIN { printf "%d", int(n * c + p + 0.999) }')}"
fi
printf 'capture: %sx%s cells at font size %s = %sx%s px\n' "$cols" "$rows" "$font_size" "$width" "$height"

launch="clear && cd $root && env -u TMUX -u TMUX_PANE -u GATE_INBOX_SESSION_ID"
launch="$launch GATE_INBOX_HOME=$home CLAUDE_CONFIG_DIR=$claude_dir CODEX_HOME=$codex_dir"
launch="$launch TMUX_TMPDIR=$sock $binary"

tape="$work/run-$run_id.tape"
{
  printf 'Output %s/board.gif\n' "$rel_media"
  printf 'Output %s/video.webm\n' "$rel_video"
  printf 'Set Width %s\nSet Height %s\nSet FontSize %s\n' "$width" "$height" "$font_size"
  printf 'Set TypingSpeed 15ms\n'
  if [[ -n "$vhs_theme" ]]; then printf 'Set Theme "%s"\n' "$vhs_theme"; fi
  if [[ -n "$vhs_theme_json" ]]; then printf 'Set Theme %s\n' "$vhs_theme_json"; fi
  printf 'Hide\n'
  printf 'Type "%s"\nEnter\nSleep 8s\n' "$launch"
  # Wait for the board before pressing anything: until its TUI has negotiated
  # the terminal's keyboard protocol, an Enter arrives as ctrl+j and the
  # welcome screen drops it — which used to feed the seeding keys to a screen
  # that was still up. First run opens on the welcome screen; Enter dismisses it.
  printf 'Enter\nSleep 3s\n'
  # Seed a board worth looking at. "n" is the one-question spawn — type the
  # CLI, Enter creates — which is the only seeding path that survives being
  # driven blind: the ctrl+n form has seven rows, and an Enter that lands on
  # the wrong one advances instead of creating. Creating focuses the new
  # session, so ctrl+q returns to the list.
  [[ $no_seed -eq 1 ]] && tools=()
  for tool in "${tools[@]}"; do
    printf 'Type "n"\nSleep 1500ms\n'
    printf 'Type "%s"\nSleep 800ms\n' "$tool"
    printf 'Enter\nSleep 6s\n'
    printf 'Ctrl+q\nSleep 2s\n'
  done
  printf 'Show\nSleep 1s\n'
  # A scenario writes its stills to {{SHOTS}}; only this driver knows the run
  # id. {{LAUNCH}} and {{TMUX}} are for the scenarios that have to leave the
  # board and come back -- a restart, or the panes going away under it -- which
  # no keystroke inside the board can produce. Ampersands in the launch line
  # are escaped because sed reads a bare & as the whole match.
  sed -e "s|{{SHOTS}}|$rel_media|g" \
    -e "s|{{LAUNCH}}|${launch//&/\\&}|g" \
    -e "s|{{TMUX}}|tmux -S $sock/tmux-$(id -u)/default|g" \
    "$scenario" | awk '
    # vhs sends Alt+x as a bare x: the escape byte a terminal puts in front of
    # an alt chord never reaches the pty, so every alt binding this board has
    # reads as its unmodified key, and Alt+, and Alt+\ do not parse at all.
    # Escape with a Type straight after it is that prefix, byte for byte, so a
    # scenario can spell an alt chord the way the keymap spells it.
    {
      line = $0
      sub(/[[:space:]]+$/, "", line)
      if (match(line, /^[[:space:]]*Alt\+[^[:space:]]$/) && substr(line, length(line)) != "\047") {
        indent = substr(line, 1, index(line, "Alt") - 1)
        printf "%sEscape\n%sType \047%s\047\n", indent, indent, substr(line, length(line))
        next
      }
      print
    }'
  # A Screenshot needs frames after it to be written, and every run owes
  # post-to-pr.ts at least one still.
  printf '\nScreenshot %s/zz-final.png\nSleep 800ms\n' "$rel_media"
} >"$tape"

(cd "$out" && PATH="$work/bin:$PATH" vhs "$tape")

printf '%s' "$run_id" >"$out/media/.latest-run"
tmux -S "$sock/tmux-$(id -u)/default" kill-server >/dev/null 2>&1 || true
rm -rf "$sock"
[[ $keep_home -eq 1 ]] || rm -rf "$home"

# vhs can drive a whole tape, print "Creating board.gif...", exit 0 and write
# none of it -- 0.12.0 does exactly that, and nothing earlier in the run
# notices: the cell probe passes because it reads a file the recorded shell
# wrote, not one vhs encoded. Unchecked, a capture ends by printing three paths
# that do not exist and post-to-pr.ts is the first thing to find out.
for artefact in "$media/board.gif" "$video_dir/video.webm" "$media/zz-final.png"; do
  [[ -s "$artefact" ]] && continue
  die "$(vhs --version 2>/dev/null || echo vhs) exited 0 without writing $artefact.
  0.12.0 records nothing at all; 0.11.0 is the last release that writes its own output:
  go install github.com/charmbracelet/vhs@v0.11.0"
done

printf '\ncapture %s\n  stills : %s\n  video  : %s\n  gif    : %s\n\n' \
  "$run_id" "$media" "$video_dir/video.webm" "$media/board.gif"
printf 'attach it to the PR from inside this checkout:\n  bun %s/skills/ship-ui-feature/scripts/post-to-pr.ts --evidence %s\n' \
  "$(cd "$root/.." && pwd)" "$out"
