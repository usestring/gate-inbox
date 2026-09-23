#!/usr/bin/env bash
# Record the README demos: each tape in tools/demo/ becomes docs/demo/<name>.gif.
#
#   tools/demo/record.sh                      # every tape
#   tools/demo/record.sh tools/demo/triage.tape
#   tools/demo/record.sh --stage-only         # build a scratch board and print how to open it
#
# Every recording runs against a board of its own: a fresh scratch tree
# mounted as /home inside a bubblewrap namespace, with the board's config,
# state and tmux server all under it and hostname "demo". The operator's home,
# tmux server and board are not visible from inside it. The sessions run
# fake-agent.sh, a scripted CLI, so a recording calls no model and reads no
# account or credential.
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd -- "$here/../.." && pwd)"
out="$root/docs/demo"
stage_only=0
keep=0
tapes=()

die() { printf 'record: %s\n' "$1" >&2; exit 1; }
missing() { printf 'record: %s\n' "$1" >&2; exit 2; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --stage-only) stage_only=1; shift ;;
    --keep) keep=1; shift ;;
    --out) out="${2:?}"; shift 2 ;;
    -h|--help) sed -n '2,13p' "${BASH_SOURCE[0]}"; exit 0 ;;
    -*) die "unknown flag $1" ;;
    *) tapes+=("$1"); shift ;;
  esac
done
[[ ${#tapes[@]} -gt 0 ]] || tapes=("$here"/*.tape)

command -v bwrap >/dev/null || missing "bwrap is not on PATH — apt-get install bubblewrap"
command -v tmux >/dev/null || missing "tmux is not on PATH"
if [[ $stage_only -eq 0 ]]; then
  command -v vhs >/dev/null || missing "vhs is not on PATH — go install github.com/charmbracelet/vhs@v0.11.0"
  command -v ttyd >/dev/null || missing "ttyd is not on PATH — apt-get install ttyd"
  command -v ffmpeg >/dev/null || missing "ffmpeg is not on PATH — apt-get install ffmpeg"
fi

# Same browser search as tools/capture/capture.sh: vhs screenshots the
# terminal through headless Chromium, which needs --no-sandbox on a box that
# hands out no user namespaces to it.
usable_chrome() { [[ -n "$1" && -x "$1" ]] && "$1" --version >/dev/null 2>&1; }
find_chrome() {
  local c
  for c in "$(command -v chrome || true)" "$(command -v google-chrome || true)" \
           "$(command -v chromium || true)" "$(command -v chromium-browser || true)" \
           "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
           "$HOME"/.cache/ms-playwright/chromium-*/chrome-linux*/chrome \
           "$HOME"/.cache/rod/browser/chromium-*/chrome; do
    usable_chrome "$c" && { printf '%s' "$c"; return 0; }
  done
  return 1
}

build="$(mktemp -d "${TMPDIR:-/tmp}/gi-demo-build.XXXXXX")"
trap 'rm -rf "$build"' EXIT
(cd "$root" && GOWORK=off nice -n 10 go build -o "$build/gate-inbox" . &&
  GOWORK=off nice -n 10 go build -o "$build/seed" ./tools/demo/seed) || die "build failed"

# stage <dir>: the scratch tree one recording runs in, and the launcher that
# opens the board inside it.
stage() {
  local s="$1" h="$1/home/user"
  mkdir -p "$h/.local/bin" "$h/.config/gate-inbox" "$h/.tmux" "$h/demo-project/src/billing" "$h/demo-project/tests"
  chmod 700 "$h/.tmux"
  cp "$build/gate-inbox" "$h/.local/bin/gate-inbox"
  cp "$here/fake-agent.sh" "$h/.local/bin/agent"

  # A small project for the agents to sit in, so the board has a branch and a
  # directory to show.
  (
    cd "$h/demo-project"
    printf '{ "name": "demo-project", "private": true }\n' >package.json
    printf 'export function exportCsv() {}\n' >src/billing/export.ts
    printf 'test("login", () => {});\n' >tests/login.test.ts
    printf '# demo-project\n' >README.md
    git init -q -b main
    git -c user.name="Demo User" -c user.email=demo@example.com add -A
    git -c user.name="Demo User" -c user.email=demo@example.com commit -qm "Initial commit"
  )

  cat >"$h/.config/gate-inbox/config.toml" <<'TOML'
poll_interval = "1s"

# The scripted demo CLI. Its rules are the claude block's, cut down to the
# lines fake-agent.sh draws; it keeps a Claude Code-shaped transcript, so the
# preview reads it as one.
[tools.agent]
command = "/home/user/.local/bin/agent"
session_id_flag = "--session-id"
session_store = "claude"
resume_by_id_command = "/home/user/.local/bin/agent"
revive_command = "/home/user/.local/bin/agent"
default_status = "idle"
mcp = "none"
skip_rename_directive = true
activity_cutoff = "(?m)^❯"
turn_end = "^✻ Worked for \\d.*$"
blocked_line = "Interrupted ·"
rules = [
  { state = "waiting", pattern = "(?m)^[ \\x{A0}]*❯[ \\x{A0}]+\\d+\\." },
  { state = "waiting", pattern = "Enter to confirm · Esc to cancel" },
  { state = "working", pattern = "esc to interrupt" },
]
TOML

  # vhs sends every key on its own, so an escape-prefixed key (f2, alt+up,
  # alt+\) reaches the board as esc followed by text. Each action a tape
  # needs gets a ctrl chord beside its default; the footers still name the
  # default.
  cat >"$h/.config/gate-inbox/keys.toml" <<'TOML'
[focus]
toggle_gate_input = ["f2", "ctrl+t"]
toggle_rail = ["alt+\\", "ctrl+b"]
preview_page_up = ["alt+pgup", "ctrl+u"]
preview_bottom = ["alt+end", "ctrl+e"]
TOML

  "$build/seed" --home "$h/.config/gate-inbox" \
    --set theme=oled --set welcome_seen=1 \
    --set default_tool=agent --set new_session_agent="default tool" \
    --set hidden_tools=claude,codex,opencode \
    --group demo-project=/home/user/demo-project

  printf 'root:x:0:0:root:/root:/bin/bash\nuser:x:%s:%s:Demo User:/home/user:/bin/bash\n' "$(id -u)" "$(id -g)" >"$s/passwd"

  # Everything the board and its sessions see: the scratch tree as /home, a
  # private /tmp, hostname "demo", and an environment built from nothing.
  cat >"$s/launch.sh" <<SH
#!/usr/bin/env bash
exec bwrap --dev-bind / / --bind "$s/home" /home --tmpfs /tmp \\
  --ro-bind "$s/passwd" /etc/passwd --unshare-uts --hostname demo \\
  --chdir /home/user/demo-project --die-with-parent \\
  env -i HOME=/home/user USER=user LOGNAME=user SHELL=/bin/bash \\
    TERM=xterm-256color COLORTERM=truecolor LANG=C.UTF-8 \\
    PATH=/home/user/.local/bin:/usr/local/bin:/usr/bin:/bin \\
    GATE_INBOX_HOME=/home/user/.config/gate-inbox TMUX_TMPDIR=/home/user/.tmux \\
    "\${@:-/home/user/.local/bin/gate-inbox}"
SH
  chmod +x "$s/launch.sh"
}

# The board's tmux server lives under the scratch tree; ending it by its socket
# path never reaches another server.
stop_board() {
  tmux -S "$1/home/user/.tmux/tmux-$(id -u)/default" kill-server >/dev/null 2>&1 || true
}

if [[ $stage_only -eq 1 ]]; then
  s="$(mktemp -d "${TMPDIR:-/tmp}/gi-demo.XXXXXX")"
  stage "$s"
  cp "$build/gate-inbox" "$s/gate-inbox"
  printf 'staged %s\n  board : %s/launch.sh\n  shell : %s/launch.sh bash\n  stop  : tmux -S %s/home/user/.tmux/tmux-%s/default kill-server\n' \
    "$s" "$s" "$s" "$s" "$(id -u)"
  trap - EXIT
  rm -rf "$build"
  exit 0
fi

chrome="$(find_chrome)" || missing "no working Chromium found — install one, or run 'bunx playwright install chromium'"
printf '#!/bin/sh\nexec "%s" --no-sandbox --disable-dev-shm-usage "$@"\n' "$chrome" >"$build/chrome"
chmod +x "$build/chrome"
mkdir -p "$out"

oled='{ "name": "oled", "background": "#000000", "foreground": "#e6e6e6", "selection": "#303030", "cursor": "#e6e6e6", "black": "#000000", "red": "#ff5f5f", "green": "#5fd75f", "yellow": "#d7d75f", "blue": "#5f87ff", "magenta": "#d787d7", "cyan": "#5fd7d7", "white": "#e6e6e6", "brightBlack": "#5f5f5f", "brightRed": "#ff8787", "brightGreen": "#87ff87", "brightYellow": "#ffff87", "brightBlue": "#87afff", "brightMagenta": "#ffafff", "brightCyan": "#87ffff", "brightWhite": "#ffffff" }'

for tape in "${tapes[@]}"; do
  [[ -r "$tape" ]] || die "no tape at $tape"
  name="$(basename "$tape" .tape)"
  s="$(mktemp -d "${TMPDIR:-/tmp}/gi-demo.XXXXXX")"
  stage "$s"
  run="$s/run.tape"
  # The tape opens hidden on a shell; it seeds what it needs, then Show.
  # vhs refuses an absolute Output path, so it runs from $out. 960px is about
  # the README column's width, so GitHub shows the GIF unscaled.
  {
    printf 'Output %s.gif\n' "$name"
    printf 'Set Shell bash\nSet FontSize 15\nSet Width 960\nSet Height 600\nSet Padding 10\n'
    printf 'Set TypingSpeed 70ms\nSet Framerate 24\nSet Theme %s\n' "$oled"
    printf 'Hide\nType "clear; exec %s/launch.sh"\nEnter\nSleep 5s\n' "$s"
    cat "$tape"
  } >"$run"
  printf 'record: %s\n' "$name"
  (cd "$out" && PATH="$build:$PATH" nice -n 10 vhs "$run") || { stop_board "$s"; die "vhs failed on $name"; }
  stop_board "$s"
  [[ -s "$out/$name.gif" ]] || die "vhs exited 0 without writing $out/$name.gif (0.12.0 does this; pin v0.11.0)"
  if [[ $keep -eq 1 ]]; then printf '  kept %s\n' "$s"; else rm -rf "$s"; fi
  printf '  %s  %s\n' "$(du -h "$out/$name.gif" | cut -f1)" "$out/$name.gif"
done
