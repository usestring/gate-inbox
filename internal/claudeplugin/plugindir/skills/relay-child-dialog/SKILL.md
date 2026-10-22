---
name: relay-child-dialog
description: Put a question or permission prompt a child session stopped on to your user word for word and key their choice back with answer_session relay true. Use when a child-dialog message arrives, when read_session shows a child waiting, or when answer_session or grant_permission refuses an answer.
---

# Relay a child's dialog

A child stopped on a dialog stays stopped until you answer it. A relayed answer is accepted only when your
own question to your user asks the child's question with its options word for word, so copy, never
retype. Every step below is mechanical; do not reword anything.

## 1. Decide whose call it is

- Header `Approval`, a permission prompt, a trust dialog: your user's. Go to step 2.
- Anything else your brief to that child or your user's standing decisions already settle: answer it
  yourself with `mcp__gate-inbox__answer_session` (`relay` false) and stop.
- Otherwise: your user's. Go to step 2.

## 2. Get the exact input

Take `relay_ask` from, in order of preference:

1. the child-dialog message you were sent (the JSON after "pass this as is to your own AskUserQuestion");
2. `mcp__gate-inbox__read_session` on the child: `digest.relay_ask`;
3. the refusal you just got: the JSON after "Next: call your own AskUserQuestion with exactly this input".

If none carries it (an older Gate Inbox), copy each unanswered entry of `digest.questions` field by field:
header, question, every option label and description in order, and multiSelect.

## 3. Ask your user

Call `AskUserQuestion` with that object as its whole input, unchanged. Do not add a recommendation, merge
questions, shorten text or drop an option. You may say what the child is doing in your own message
before the call, never inside it.

## 4. Key the answer back

Call `mcp__gate-inbox__answer_session` on the child with `relay` true:

- one question: `answer` set to the label your user picked, or the words they typed;
- several questions: `answers`, one entry per question, `question` set to its index or header;
- a multi-select: `ticks` with every label they left ticked.

Never pass an answer your user did not give, and never answer before they have.

## 5. When it is refused

Read the refusal and do exactly what it says; do not retry the same call.

| Refusal says | Do |
|---|---|
| "Next: call your own AskUserQuestion with exactly this input" | Step 3 with that input, then step 4. |
| "this is the child asking for your user's approval" | You answered without relaying. Steps 2 to 4. |
| "your user answered X, not Y" | Key X, the answer they gave. Do not ask again. |
| "already been relayed once" or "before the child asked" | Ask again: steps 2 to 4. |
| "no dialog up: ... status is S" | `mcp__gate-inbox__wait_for_session` on the child until it is waiting, then step 2. |
| "this grant needs your user's approval" (`grant_permission`) | Pass the JSON after the colon to `AskUserQuestion` as is; once they choose Grant, call `grant_permission` again with the same arguments. |

A refusal that names none of these is a real problem: tell your user what the child is waiting on.
