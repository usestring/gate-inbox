#!/usr/bin/env bash
# Write the provenance manifest a capture's uploader checks before it publishes
# anything: the scratch tmux server, board home and HOME the recording ran
# against, and the sha256 of every file it wrote. A capture with no manifest, or
# one naming the operator's own server, board or HOME, is never published.
#
#   tools/capture/manifest.sh <manifest> <run-id> <tmux-tmpdir> <board-home> <home> <file>...
set -euo pipefail

[[ $# -ge 6 ]] || { echo "usage: manifest.sh <manifest> <run-id> <tmux-tmpdir> <board-home> <home> <file>..." >&2; exit 64; }
manifest="$1" run_id="$2" tmux_tmpdir="$3" board_home="$4" home="$5"
shift 5
trap 'rm -f "$manifest.tmp"' EXIT

json() { local s="${1//\\/\\\\}"; printf '"%s"' "${s//\"/\\\"}"; }
digest() {
  if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

{
  printf '{\n  "version": 1,\n  "producer": "gate-inbox/tools/capture",\n  "run_id": %s,\n  "fixture": true,\n' "$(json "$run_id")"
  printf '  "tmux_tmpdir": %s,\n  "gate_inbox_home": %s,\n  "home": %s,\n  "files": [' \
    "$(json "$tmux_tmpdir")" "$(json "$board_home")" "$(json "$home")"
  sep=""
  for file in "$@"; do
    [[ -s "$file" ]] || { echo "manifest: $file is missing or empty" >&2; exit 1; }
    printf '%s%s' "$sep" "$(json "$(digest "$file")")"
    sep=", "
  done
  printf ']\n}\n'
} >"$manifest.tmp"
mv "$manifest.tmp" "$manifest"
