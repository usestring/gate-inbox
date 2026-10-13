---
description: List stale Gate Inbox sessions and undelivered mail, each with a proposed archive, revive, kill or check. Proposes only; changes nothing.
argument-hint: "[--idle 72h] [--waiting 24h] [--errored 24h] [--dead 1h] [--mail 1h]"
---

Run this once with the Bash tool, passing any thresholds the user gave:

```
"$GATE_INBOX_BIN" sweep --json $ARGUMENTS
```

It reads the board's state.db read-only. If `$GATE_INBOX_BIN` is empty, this session is not on a Gate Inbox board: say so and stop.

Present the result to the user:

1. One line with the counts per action and how many rows were scanned.
2. A table per action (archive, revive, kill, check) with session id, name, status, age, top-level or parent, and the reason.
3. Under a waiting row, its question; under a row with mail, each message's id, sender and age.
4. The exact call for each row from its `calls` field, in a code block the user can copy.

Do not call archive_session, revive_session, kill_session, message_status or any other tool the report names, and do not answer a held dialog. This command only proposes. The user decides which proposals to carry out and asks for them in a later message. Kill and archive end a running agent, so name the session when the user asks for either.
