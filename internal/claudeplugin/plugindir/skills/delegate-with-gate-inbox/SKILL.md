---
name: delegate-with-gate-inbox
description: Use before handing any real work (an investigation, implementation, review or multi-step search) to another agent, instead of the Agent, Task or Workflow tool, and whenever a session you created stops on a question or permission dialog. Covers create_session, list_sessions, send_session, wait_for_session and answer_session.
---

# Delegating work: use Gate Inbox sessions

Open a new session only when your user explicitly asks for one; otherwise ask them first. Do the work yourself, or hand it to a session already on the board: call list_sessions and send_session to the one that owns that workstream, idle or working, with interrupt true when its current turn is wrong or superseded.

When your user does ask for another agent, start it with create_session instead of the built-in Agent tool (also called Task: general-purpose, Explore, Plan, fork and custom agents) or the Workflow tool. A session is on the user's board, and its questions are relayed to you. Keep the built-in tool for a quick read-only lookup.

Give create_session a name and a prompt that states the whole task; the new agent cannot see this conversation. Track shared plans with the task tool. Check on it with read_session, redirect it with send_session, block on it with wait_for_session, and archive_session when done. One session per workstream.

If the gate-inbox tools are missing, use the built-in Agent tool as usual.

# Your children's dialogs are yours

When a session you created stops on a dialog, Gate Inbox forwards it to you. Answer what your brief or your user's standing decisions settle. Ask your user the rest with your own question tool, copying each question and its options word for word, then reply with answer_session, marking the answer as your user's own. Permission prompts, trust dialogs and questions headed Approval are always your user's to answer. Never tell your user to answer at the child's pane, and never leave a child waiting unmentioned.
