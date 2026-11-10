#!/usr/bin/env bash
# Write the vhs tape a capture runs, to stdout, from a scenario.
#
#   tools/capture/runtape.sh <scenario> <rel-media> <rel-video> [tool...]
#
# The run tape sits beside the recording, and the uploader scans every file in
# the run before it publishes anything to a public PR. A tape that spelled out
# the scratch tmux directory, board home or fixture HOME tripped that scan on
# every capture, even though none of those paths is the operator's. So the
# tape names nothing absolute at all: capture.sh puts a launcher on vhs's PATH
# (gib-cap-launch) and exports the two scratch paths a scenario may need, and
# the tape refers to those by name. Every Output path is relative to the run
# directory vhs starts in.
#
# The scenario's placeholders become:
#   {{SHOTS}}  <rel-media>, where its stills go
#   {{LAUNCH}} clear && gib-cap-launch, which starts the board on scratch state
#   {{TMUX}}   tmux -S $GIB_CAP_TMUX, the board's own tmux server; unquoted,
#              because scenarios use it inside a Type "..." string, and the
#              scratch socket path never has a space in it
#   {{HOME}}   $GIB_CAP_HOME, the scratch board home
# so a scenario can only reach them from the shell, before or after the board.
#
# Sizing and theme come from the environment, because they are numbers and
# colours rather than paths: CAP_WIDTH, CAP_HEIGHT, CAP_FONT_SIZE, and
# optionally CAP_VHS_THEME (a vhs theme name) or CAP_VHS_THEME_JSON.
set -euo pipefail

[[ $# -ge 3 ]] || { echo "usage: runtape.sh <scenario> <rel-media> <rel-video> [tool...]" >&2; exit 64; }
scenario="$1" rel_media="$2" rel_video="$3"
shift 3
for rel in "$rel_media" "$rel_video"; do
  [[ "$rel" != /* ]] || { echo "runtape: $rel must be relative to the run directory" >&2; exit 64; }
done

launch='clear \&\& gib-cap-launch'

printf 'Output %s/board.gif\n' "$rel_media"
printf 'Output %s/video.webm\n' "$rel_video"
# The screen as text, for the pre-upload scan; it is never uploaded.
printf 'Output %s/board.txt\n' "$rel_media"
printf 'Set Width %s\nSet Height %s\nSet FontSize %s\n' "${CAP_WIDTH:?}" "${CAP_HEIGHT:?}" "${CAP_FONT_SIZE:?}"
printf 'Set TypingSpeed 15ms\n'
if [[ -n "${CAP_VHS_THEME:-}" ]]; then printf 'Set Theme "%s"\n' "$CAP_VHS_THEME"; fi
if [[ -n "${CAP_VHS_THEME_JSON:-}" ]]; then printf 'Set Theme %s\n' "$CAP_VHS_THEME_JSON"; fi
printf 'Hide\n'
printf 'Type "clear && gib-cap-launch"\nEnter\nSleep 8s\n'
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
for tool in "$@"; do
  printf 'Type "n"\nSleep 1500ms\n'
  printf 'Type "%s"\nSleep 800ms\n' "$tool"
  printf 'Enter\nSleep 6s\n'
  printf 'Ctrl+q\nSleep 2s\n'
done
printf 'Show\nSleep 1s\n'
sed -e "s|{{SHOTS}}|$rel_media|g" \
  -e 's|{{HOME}}|$GIB_CAP_HOME|g' \
  -e "s|{{LAUNCH}}|$launch|g" \
  -e 's|{{TMUX}}|tmux -S $GIB_CAP_TMUX|g' \
  "$scenario" | awk '
  # vhs sends Alt+x as a bare x: the escape byte a terminal puts in front of
  # an alt chord never reaches the pty, so every alt binding this board has
  # reads as its unmodified key, and Alt+, and Alt+\ do not parse at all.
  # Escape with a Type straight after it is that prefix, byte for byte, so a
  # scenario can spell an alt chord the way the keymap spells it.
  #
  # Comments in a scenario are for whoever reads it, not for vhs, and they are
  # where a usage line naming a local path ends up, so they are dropped. No
  # apostrophes in here: this program is one single-quoted shell word.
  /^[[:space:]]*#/ { next }
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
