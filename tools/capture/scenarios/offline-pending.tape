# A send made while the machine is offline waits in the outbox, and the row
# it is held for reads ◍ pending. Run with the network probe pointed at a
# dead proxy so the board reads offline, and the opencode-finished transcript
# so demo-project-3 rests finished:
#
#   HTTPS_PROXY=http://127.0.0.1:9 tools/capture/capture.sh \
#     --transcripts waiting,codex-working,opencode-finished \
#     --scenario tools/capture/scenarios/offline-pending.tape
#
# The operator's line is queued from a terminal with `send --as-human`; the
# board holds it instead of typing it into the pane, so the finished row turns
# pending and the message stays queued.

Sleep 3s
Type "T"
Sleep 5s
Type "PS1='$ '"
Enter
Sleep 500ms
# The terminal's own id lets it list the board; the recorder's CLAUDECODE
# would make it an agent, and an agent may not speak as the operator.
Type `unset CLAUDECODE; export GATE_INBOX_SESSION_ID=$(tmux display -p '#S' | sed 's/^gi_//')`
Enter
Sleep 1s
Type `target=$("${GATE_INBOX_BIN:-gate-inbox}" sessions --json | python3 -c 'import json,sys;w=lambda o:[x for v in (o.values() if isinstance(o,dict) else o if isinstance(o,list) else []) for x in w(v)]+([o["id"]] if isinstance(o,dict) and o.get("name")=="demo-project-3" else []);print(w(json.load(sys.stdin))[0])')`
Enter
Sleep 1s
Type `"${GATE_INBOX_BIN:-gate-inbox}" send --as-human "$target" "rebase on main and rerun the tests"`
Enter
Sleep 2s
Type "clear"
Enter
Sleep 1s
Ctrl+q
Sleep 5s
Screenshot {{SHOTS}}/01-finished-row-reads-pending-while-offline.png
Sleep 1s

# The key map opens under h with its search on; leave the search before l
# can reach the legend.
Type "h"
Sleep 2s
Enter
Sleep 600ms
Type "l"
Sleep 2s
Screenshot {{SHOTS}}/02-legend-explains-pending.png
Sleep 1s
