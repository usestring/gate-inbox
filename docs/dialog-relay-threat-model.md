# Answering a child's dialog: threat model

`answer_session` lets the session that spawned a child answer the dialog the child has stopped
on. Claude Code passes an AskUserQuestion answer to its auto-mode classifier as the user's own
input, and a permission or trust choice decides what the child may do on the user's machine, so
an answer a parent keys can stand in for the user. This page says what Gate Inbox lets a parent
key, what it checks first, and what it records. The code is `internal/sessioncmd/relay.go` and
`internal/sessioncmd/answerscreen.go`.

## What a parent may answer

| Dialog | Parent's own answer | Relayed answer (`relay: true`) |
| --- | --- | --- |
| AskUserQuestion, single or several questions | Keyed, recorded as `agent` | Keyed after verification, recorded as `relayed_user` |
| AskUserQuestion multi-select (`ticks`) | Keyed, recorded as `agent` | Keyed after verification, recorded as `relayed_user` |
| A question headed `Approval` | Refused | Keyed after verification |
| Permission prompt (Bash, Edit, Read, WebFetch and the rest, including "don't ask again" choices) | Refused | Keyed after verification |
| Workspace-trust, MCP-server trust, Codex directory trust, any other numbered-choice screen | Refused | Keyed after verification |
| A screen with no choices this can read (`keys`) | Refused | Keyed after verification |

A permission or trust choice is never an agent's. There is no argument, flag or wording that lets a
parent pick one on its own judgement.

## What verification checks

For every relayed answer, the parent's own transcript must hold an AskUserQuestion that:

- asks the child's dialog word for word. For a question, that is its text and its option labels in
  order. For a permission or trust screen, the question must contain the dialog's text (the tool,
  the command or path, the prompt) and its options must be the dialog's choices. For `keys`, the
  question must quote the screen. The comparison ignores whitespace and case only, because the
  child's pane breaks long paths and commands inside a word at its own width;
- was answered after the child stopped on the dialog. For a question, that is the time of the
  child's AskUserQuestion call. For a screen, it is when the child last turned waiting, or its
  launch;
- carries the answer being keyed. A screen's choice must be one of its labels, matched by text and
  never by position. `keys` must be the exact keys the user picked;
- has not already been relayed for this dialog. Each piece of evidence is spent once;
- was not itself typed into the parent by Gate Inbox on another agent's behalf.

A refusal keys nothing and says exactly what to ask the user and how to call again.

## What answering checks after the keys

- A question reads back what the child registered (the review page, the printed record), and a
  mismatch is an error.
- A multi-select toggles each box one at a time, sees each toggle land, then reads the boxes back
  as a whole and refuses to submit anything but the exact set.
- A screen choice moves the marker by arrows, sees it on the named choice before Enter, and sees
  the dialog clear after.
- `keys` sees the screen change.

## Adversaries

| Adversary | Attempt | Outcome |
| --- | --- | --- |
| Confused parent | Relays a paraphrased, broadened or remembered answer | Refused: the dialog, the options and the answer must match the user's own dialog, and the evidence is single-use |
| Confused parent | Answers an Approval question, a permission prompt or a trust dialog without relay | Refused, with the exact question to ask |
| Confused parent | Answers a permission prompt by a choice's number or position | Refused: a choice is named by its text, and a number is no label |
| Confused parent | Relays "Yes, delete everything" to a prompt whose choice is "Yes" | Refused: a screen choice must be a label or the start of exactly one, never found inside a longer answer |
| Hijacked parent | Answers a non-Approval question without relay | Keyed, but recorded as `agent`, and the child's classifier is told it is not the user's approval |
| Hijacked parent | Sends `keys` that type a command | Refused: only navigation keys, Enter, Escape and one letter or digit are sent, at most twelve |
| Hijacked parent that is itself a child | Uses an answer its own spawner typed as evidence | Refused, because the ledger shows an agent typed it |
| Hijacked parent | Writes fake JSONL into its own transcript, or drives tmux directly | Mitigated, not prevented: the sandbox denies writes to `~/.claude/projects`, and the parent's classifier reviews unsandboxed writes and tmux self-driving. A residual risk for anything running as the same user |
| Spawn brief or `send --as-human` | Writes an approval into the child's first prompt or an unfenced turn | Not closed here |

## Residual notes

- A screen relay's "after the child stopped" bound is the child's last status change, read by the
  poller every few seconds. A user answer given in the seconds before the poller saw the dialog is
  refused, which fails safe.
- The relay path depends on Claude Code treating AskUserQuestion answers as user input, which is
  observed but not documented.
