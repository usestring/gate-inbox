#!/usr/bin/env bash
# WHY: a skill's `gate-inbox-priority:` frontmatter states how much its work matters, and only the
# session running it can tell the Gate Inbox. Fires on the Skill tool and on a typed /skill prompt,
# declares the tier from inside the session, and never blocks: a missing board, skill, or key
# declares nothing.
set -uo pipefail

input="$(cat)"

# WHY: a launcher can own the session's priority, so skills must leave that tier alone.
[[ "${GATE_INBOX_PRIORITY_LOCKED:-}" == "1" ]] && exit 0

bin="${GATE_INBOX_BIN:-${AGENT_MANAGER_BIN:-}}"
[[ -n "$bin" ]] || exit 0

case "$(printf '%s' "$input" | jq -r '.hook_event_name // empty' 2>/dev/null)" in
  UserPromptSubmit)
    skill="$(printf '%s' "$input" | jq -r '.prompt // empty' 2>/dev/null \
      | sed -nE '1s#^[[:space:]]*/([A-Za-z0-9_.:-]+).*#\1#p')"
    ;;
  *)
    [[ "$(printf '%s' "$input" | jq -r '.tool_name // empty' 2>/dev/null)" == "Skill" ]] || exit 0
    skill="$(printf '%s' "$input" | jq -r '.tool_input.skill // empty' 2>/dev/null)"
    ;;
esac
skill="${skill##*:}"
[[ "$skill" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || exit 0

file=""
for root in "${CLAUDE_PROJECT_DIR:-}/skills" "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/skills"; do
  [[ -f "$root/$skill/SKILL.md" ]] && { file="$root/$skill/SKILL.md"; break; }
done
[[ -n "$file" ]] || exit 0

tier="$(awk '
  NR == 1 && !/^---[[:space:]]*$/ { exit }
  NR > 1 && /^---[[:space:]]*$/ { exit }
  /^gate-inbox-priority:/ {
    sub(/^gate-inbox-priority:[[:space:]]*/, ""); sub(/[[:space:]]*#.*$/, ""); gsub(/["'\'']/, "")
    print; exit
  }
' "$file")"
case "$tier" in
  urgent|high|medium|low) "$bin" priority "$tier" >/dev/null 2>&1 ;;
esac
exit 0
