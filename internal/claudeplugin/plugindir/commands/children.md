---
description: List your child sessions, and put every question one of them is waiting on to your user
allowed-tools: Bash("$GATE_INBOX_BIN" children:*)
---

## Your children

!`"$GATE_INBOX_BIN" children`

## What to do

For each child above that is waiting:

1. If your brief or your user's standing decisions settle the question, answer it with
   `answer_session` and say what you answered.
2. Otherwise put it to your user. Call AskUserQuestion with the child's header, question and options
   copied word for word, previews included, and with multiSelect when the child set it. Then
   pass your user's answer to `answer_session` with `relay: true`, which marks it as your user's own. A question headed Approval
   is always your user's to answer.
3. A permission prompt is always your user's call. Read it with `read_session`, put it to your user word
   for word, then pass their choice to `answer_session` with `relay: true`. `grant_permission`, for a step
   the child's permission system refused, asks your user too.

Batch the relayed questions into one AskUserQuestion call, up to four at a time. If no child
is waiting, say so in one line.
