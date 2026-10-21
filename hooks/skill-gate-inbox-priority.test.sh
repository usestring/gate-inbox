#!/usr/bin/env bash
# Tests for skill-gate-inbox-priority.sh — drives the hook with synthetic payloads against a
# scratch skills tree and a stub board binary that records what it was asked to declare.
set -euo pipefail

HOOK="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/skill-gate-inbox-priority.sh"
fails=0

scratch="$(mktemp -d "${TMPDIR:-/tmp}/skill-gate-inbox-priority-test.XXXXXX")"
[ -n "$scratch" ] || { echo "mktemp -d failed" >&2; exit 1; }
trap 'rm -rf "$scratch"' EXIT

export CLAUDE_PROJECT_DIR="$scratch/project"
export CLAUDE_CONFIG_DIR="$scratch/claude"
unset GATE_INBOX_BIN AGENT_MANAGER_BIN GATE_INBOX_PRIORITY_LOCKED
log="$scratch/declared"
stub="$scratch/gate-inbox"
printf '#!/usr/bin/env bash\necho "$*" >> %q\n' "$log" > "$stub"
chmod +x "$stub"

skill() {
  local root="$1" name="$2" key="$3"
  mkdir -p "$root/$name"
  printf -- '---\nname: %s\n%beffort: low\n---\n\ngate-inbox-priority: urgent\n' "$name" "$key" > "$root/$name/SKILL.md"
}
skill "$CLAUDE_PROJECT_DIR/skills" mail-review 'gate-inbox-priority: high\n'
skill "$CLAUDE_PROJECT_DIR/skills" board-sweep "gate-inbox-priority: 'low'  # background\n"
skill "$CLAUDE_PROJECT_DIR/skills" plain ''
skill "$CLAUDE_PROJECT_DIR/skills" typo 'gate-inbox-priority: hihg\n'
skill "$CLAUDE_CONFIG_DIR/skills" home-only 'gate-inbox-priority: medium\n'

check() {
  local want="$1" name="$2" payload="$3"; shift 3
  : > "$log"
  printf '%s' "$payload" | env "$@" "$HOOK" >/dev/null 2>&1 || { echo "FAIL - $name (nonzero exit)"; fails=$((fails + 1)); return; }
  local got; got="$(cat "$log")"
  if [ "$want" = "$got" ]; then echo "ok   - $name"; else echo "FAIL - $name (want '$want', got '$got')"; fails=$((fails + 1)); fi
}
tool() { printf '{"hook_event_name":"PreToolUse","tool_name":"Skill","tool_input":{"skill":"%s"}}' "$1"; }
prompt() { printf '{"hook_event_name":"UserPromptSubmit","prompt":"%s"}' "$1"; }

check "priority high" "Skill tool declares the skill's tier" "$(tool mail-review)" GATE_INBOX_BIN="$stub"
check "priority high" "an unlocked session can declare" "$(tool mail-review)" GATE_INBOX_BIN="$stub" GATE_INBOX_PRIORITY_LOCKED=0
check "priority low" "quoted value with a trailing comment" "$(tool board-sweep)" GATE_INBOX_BIN="$stub"
check "priority high" "typed /skill prompt declares" "$(prompt '/mail-review now')" GATE_INBOX_BIN="$stub"
check "priority medium" "falls back to the user skills dir" "$(tool home-only)" GATE_INBOX_BIN="$stub"
check "priority high" "plugin-qualified name resolves" "$(tool example:mail-review)" GATE_INBOX_BIN="$stub"
check "priority high" "agent-manager board binary" "$(tool mail-review)" AGENT_MANAGER_BIN="$stub"
check "" "no key -> nothing (body text is not frontmatter)" "$(tool plain)" GATE_INBOX_BIN="$stub"
check "" "unknown tier -> nothing" "$(tool typo)" GATE_INBOX_BIN="$stub"
check "" "unknown skill -> nothing" "$(tool missing)" GATE_INBOX_BIN="$stub"
check "" "prompt without a slash command -> nothing" "$(prompt 'run mail-review')" GATE_INBOX_BIN="$stub"
check "" "other tool -> nothing" '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}' GATE_INBOX_BIN="$stub"
check "" "path traversal in the name -> nothing" "$(tool '../mail-review')" GATE_INBOX_BIN="$stub"
check "" "dot-only name -> nothing" "$(tool '..')" GATE_INBOX_BIN="$stub"
check "" "locked session keeps its launcher's tier" "$(tool mail-review)" GATE_INBOX_BIN="$stub" GATE_INBOX_PRIORITY_LOCKED=1

mkdir -p "$scratch/consumer/hooks"
ln -s "$HOOK" "$scratch/consumer/hooks/skill-gate-inbox-priority.sh"
canonical_hook="$HOOK"
HOOK="$scratch/consumer/hooks/skill-gate-inbox-priority.sh"
check "priority high" "a symlink invokes the canonical hook" "$(tool mail-review)" GATE_INBOX_BIN="$stub"
HOOK="$canonical_hook"

: > "$log"
rc=0; tool mail-review | env -u GATE_INBOX_BIN -u AGENT_MANAGER_BIN "$HOOK" >/dev/null 2>&1 || rc=$?
if [ "$rc" = 0 ] && [ ! -s "$log" ]; then echo "ok   - no board -> exit 0, nothing declared"; else echo "FAIL - no board"; fails=$((fails + 1)); fi

[ "$fails" -eq 0 ] || { echo "$fails failure(s)"; exit 1; }
echo "all passed"
