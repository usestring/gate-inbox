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
   pass your user's answer to `answer_session`, marked as your user's own answer. A question headed Approval
   is always your user's to answer.
3. For a permission prompt, read it with `read_session`, then `grant_permission` or put it to your user.

Batch the relayed questions into one AskUserQuestion call, up to four at a time. If no child
is waiting, say so in one line.
