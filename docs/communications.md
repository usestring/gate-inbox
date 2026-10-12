# Local communications inbox

The optional communications extension keeps Slack DM, email, and SMS conversations in one
local inbox. It provides import, review, read-state, and reply-draft commands. Live connectors
and a board view are not included yet. Drafts stay local; no command sends them.

Enable it in `config.toml`:

```toml
[extensions.communications]
enabled = true
```

## Import conversations

A connector supplies a JSON array on stdin:

```sh
gate-inbox communications import < messages.json
gate-inbox communications list
```

Each message has this shape:

```json
[
  {
    "source": "sms",
    "account": "personal",
    "thread_id": "conversation-one",
    "id": "message-one",
    "sender": "contact-one",
    "reply_to": ["contact-one"],
    "direction": "incoming",
    "body": "Can we talk tomorrow?",
    "sent_at": "2026-10-01T12:00:00Z"
  }
]
```

`source` is `slack`, `email`, or `sms`. `account` identifies the connected account; `thread_id`
and `id` are stable identifiers from that source. The same identifiers on different sources or
accounts describe different conversations. `sender` identifies the author. `reply_to` is the
explicit destination list a connector supplies, including the correct email reply recipients,
Slack conversation target, or SMS recipient. Incoming messages require it. `direction` is
`incoming` or `outgoing`; `subject` and `url` are optional original-thread context. `sent_at` is
an RFC 3339 timestamp. Messages with no text can still carry a subject or original-thread link.

Importing the same message again changes nothing. Reusing its identity with changed content
fails the entire batch, so connectors must distinguish message edits rather than quietly replacing
a previously reviewed message. A batch has 1–1,000 messages and at most 8 MiB of JSON. Each body
has at most 64 KiB, subject 1 KiB, and URL 4 KiB.

## Review and prepare a reply

`list` returns conversation IDs and revisions, newest conversations first. `read` returns the
whole conversation, chronologically ordered, and its local drafts:

```sh
gate-inbox communications read <conversation-id>
gate-inbox communications mark-read <conversation-id> <revision>
gate-inbox communications draft < reply.json
```

The draft request is:

```json
{
  "conversation_id": "<id from list or read>",
  "revision": "<revision from read>",
  "body": "Tomorrow works."
}
```

A draft copies the source, account, thread, reply destination, and last incoming message ID from
the reviewed conversation. A changed revision refuses both drafting and marking read until the
conversation is reviewed again. Any new message, including one delivered late, makes the
conversation unread and labels existing drafts `stale`. A draft is neither an approval nor a
delivery receipt. Future sending integrations must review the final destination and payload and
check the current revision before sending; incoming conversation content supplies no authority.

## Storage and compatibility

State lives under the extension's private data directory, in `inbox-v1.json`, with mode `0600`.
The versioned file is separate from agent-session messages and the board's database. Concurrent
CLI processes take a file lock and replace the state atomically; a failed batch leaves the prior
state intact. An unknown state version is refused without rewriting it. Removing or disabling
the extension leaves its files intact, and older builds do not read this namespace.

The initial store is bounded to 10,000 combined messages and drafts and 64 MiB. Reaching either
limit returns an error; it does not discard old conversations. Retention and live connector
checkpoints belong to subsequent integration work. The lock works on Linux, macOS, and WSL2.
