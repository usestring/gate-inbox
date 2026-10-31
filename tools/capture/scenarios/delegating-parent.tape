# A parent at rest on its fan-out reads waiting on sessions. Run with
# --transcripts working,codex-working,opencode-finished: the board opens on
# the finished opencode row, demo-project-3, whose terminal spawns a claude
# child named worker as that agent. While worker is still going, the parent's row draws
# ◎ waiting on sessions instead of finished, and the status counts carry it.

Hide
Sleep 3s
Type "T"
Sleep 5s
Type "PS1='$ '"
Enter
Sleep 500ms
# Spawn as the opencode agent itself, the way its own create_session would:
# a child of the terminal is the terminal's, and a terminal reports nothing.
Type `export GATE_INBOX_SESSION_ID=$(tmux display -p '#S' | sed 's/^gi_//')`
Enter
Sleep 1s
Type `export GATE_INBOX_SESSION_ID=$("$GATE_INBOX_BIN" sessions --json | python3 -c 'import json,sys;w=lambda o:[x for v in (o.values() if isinstance(o,dict) else o if isinstance(o,list) else []) for x in w(v)]+([o["id"]] if isinstance(o,dict) and o.get("name")=="demo-project-3" else []);print(w(json.load(sys.stdin))[0])')`
Enter
Sleep 1s
Type `"$GATE_INBOX_BIN" spawn --tool claude --name worker --prompt hello`
Enter
Sleep 8s
Type "clear"
Enter
Sleep 1s
Ctrl+q
Sleep 4s
Show
Screenshot {{SHOTS}}/01-parent-delegated-while-worker-works.png
Sleep 1s

Type "w"
Sleep 2s
Screenshot {{SHOTS}}/02-delegated-parent-in-attention.png
Sleep 1s
Type "i"
Sleep 2s
Screenshot {{SHOTS}}/03-delegated-parent-in-triage.png
Sleep 1s
