---
name: delegate-with-gate-inbox
description: Use before handing any real work (an investigation, implementation, review or multi-step search) to another agent, instead of the Agent, Task or Workflow tool, and whenever a session you created stops on a question or permission dialog. Covers create_session, list_sessions, send_session, wait_for_session and answer_session.
---

# Delegating work: use Gate Inbox sessions

The gate-inbox server's instructions say when to delegate and to whom. For a new delegated workstream, start it with create_session instead of the built-in Agent tool (also called Task: general-purpose, fork and any custom agent that can edit files) or the Workflow tool. A session is on the user's board, and its questions are relayed to you. Keep the built-in tool for a quick read-only lookup.

Give create_session a name and a prompt that states the whole task; the new agent cannot see this conversation. Track shared plans with the task tool. Check on it with read_session, redirect it with send_session and block on it with wait_for_session. A finished child archives on its own after a grace period; archive_session files one sooner. One session per workstream.

If the gate-inbox tools are missing, use the built-in Agent tool as usual.

# Your children's dialogs are yours

When a session you created stops on a dialog, Gate Inbox relays it to you. Answer what your brief or your user's standing decisions settle. Ask your user the rest with your own question tool, copying each question and its options word for word, then reply with answer_session and relay: true. Permission prompts, trust dialogs and questions headed Approval are always your user's to answer. Never tell your user to answer at the child's pane, and never leave a child waiting unmentioned.
